package cli

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/spec"
)

// PermissionRule is what a project must allow for blip to run unprompted.
const PermissionRule = "Bash(blip:*)"

const initLong = `Write a .blip.toml for this repo.

blip probes the base URL for a spec at the usual paths and records what it finds,
so the config it writes is one you can use rather than a template to fill in.

It also leaves a note in CLAUDE.md and a permission rule in .claude/settings.json,
because an agent that has not been told the tool exists will never reach for it.
Pass --no-claude to skip that. Nothing already written is overwritten: an existing
settings.json is reported rather than edited, so a merge is always yours to make.`

const claudeNote = `## Calling this API with blip

This repo has a blip config at ` + "`.blip.toml`" + `. Run ` + "`blip describe --compact`" + ` once to
see every operation. Call one with ` + "`blip <group> <op>`" + `, or ` + "`blip call <operationId>`" + `
for a name that does not move when the spec is regenerated.

Never pass ` + "`--env`" + `, ` + "`--config`" + ` or ` + "`--profile`" + `: those choose which service is
called and which credential is sent, and this repo is already configured. Before any
POST, PUT, PATCH or DELETE, run the command with ` + "`--dry-run`" + ` and read what it would
send, then run it again with ` + "`--yes`" + `.

Credentials are not in this repo and must not be added to it.
`

func newInitCommand(rt *Runtime) *cobra.Command {
	var (
		name       string
		envName    string
		authName   string
		withClaude bool
		noClaude   bool
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "init <base-url>",
		Short: "Write a .blip.toml for this repo",
		Long:  initLong,
		Args:  usageArgs(cobra.ExactArgs(1)),
		Example: `  blip init https://localhost:7161
  blip init https://api.example.internal --auth myapi-dev
  blip init https://localhost:5001 --name orders --no-claude`,
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := parseBase(args[0])
			if err != nil {
				return err
			}

			root, err := rt.repoRoot()
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(root)
			}

			target := filepath.Join(root, config.Names[0])
			if _, err := os.Stat(target); err == nil && !force {
				return usageError("%s already exists; edit it, or pass --force to replace it", target)
			}

			if rt.Globals.DryRun {
				fmt.Fprintf(rt.Stdout, "# would write %s\n%s", target, renderConfig(name, envName, authName, base, ""))
				return nil
			}

			specURL := rt.probeSpec(cmd, base)
			body := renderConfig(name, envName, authName, base, specURL)
			if err := replace(target, []byte(body), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(rt.Stderr, "blip: wrote %s\n", target)

			if withClaude && !noClaude {
				if err := rt.writeClaudeNote(root); err != nil {
					return err
				}
				if err := rt.writePermission(root); err != nil {
					return err
				}
			}
			rt.nextSteps(authName, specURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "API name, used as the cache key (default: the repo directory name)")
	cmd.Flags().StringVar(&envName, "env", "dev", "name of the environment to write")
	cmd.Flags().StringVar(&authName, "auth", "", "credentials profile this environment should use")
	cmd.Flags().BoolVar(&withClaude, "claude", true, "also write the CLAUDE.md note and the permission rule")
	cmd.Flags().BoolVar(&noClaude, "no-claude", false, "skip the CLAUDE.md note and the permission rule")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing .blip.toml")
	return cmd
}

func parseBase(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, usageError("%q is not a URL: %v", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, usageError("base URL must be http or https, got %q", raw)
	}
	if u.Host == "" {
		return nil, usageError("base URL %q has no host", raw)
	}
	// base_url is committed, and probing would put these on the wire first.
	if u.User != nil {
		return nil, usageError("base URL carries credentials; put them in ~/.config/blip/credentials.toml and pass --auth instead")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, usageError("base URL must not carry a query or fragment; %q is a request, not a base", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

// repoRoot is where a committed config belongs: beside .git, not wherever the
// caller happens to be standing.
func (rt *Runtime) repoRoot() (string, error) {
	dir := rt.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	home, _ := os.UserHomeDir()
	for current := dir; ; {
		// The home directory is only a candidate when the caller is standing in
		// it. Walking up into a dotfiles repo would put the permission rule in
		// the machine-wide .claude/settings.json.
		reachedHomeFromBelow := home != "" && current == home && current != dir
		if !reachedHomeFromBelow {
			if info, err := os.Lstat(filepath.Join(current, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
				return current, nil
			}
		}
		parent := filepath.Dir(current)
		if parent == current || reachedHomeFromBelow {
			return dir, nil
		}
		current = parent
	}
}

// writeNew creates a file that must not already exist. O_EXCL is what stops a
// symlink redirecting the write out of the repo: a hostile repo can ship one,
// and every path init writes to is a name that repo chose.
func writeNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// replace overwrites a file the caller has already decided may be replaced,
// after refusing a symlink standing in its place.
func replace(path string, data []byte, mode os.FileMode) error {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return writeNew(path, data, mode)
	case info.Mode()&os.ModeSymlink != 0:
		return output.Configf("%s is a symlink; refusing to write through it", path)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// probeSpec reports the spec path worth recording. An empty result is not a
// failure: with no spec_url blip probes the same paths on every run.
func (rt *Runtime) probeSpec(cmd *cobra.Command, base *url.URL) string {
	env := &config.Environment{
		Name:     "init",
		BaseURL:  base,
		Insecure: config.IsLocalHost(base.Hostname()),
		Timeout:  config.DefaultTimeout,
	}
	client, err := request.NewClient(env, rt.Globals.Timeout)
	if err != nil {
		rt.Warnf("%v", err)
		return ""
	}

	found, ok := spec.Probe(cmd.Context(), client, base, append(append([]string{}, spec.ProbePaths...), spec.ExtraProbePaths...))
	if !ok {
		rt.Warnf("no spec found at %s; blip will probe again on each run, or set spec_url, or declare [[route]] entries", base)
		return ""
	}

	path := strings.TrimPrefix(found, base.String())
	fmt.Fprintf(rt.Stderr, "blip: found a spec at %s\n", found)
	if isProbePath(path) {
		// One of the paths blip already tries, so recording it buys nothing.
		return ""
	}
	return path
}

func isProbePath(path string) bool {
	for _, p := range spec.ProbePaths {
		if p == path {
			return true
		}
	}
	return false
}

func renderConfig(name, envName, authName string, base *url.URL, specURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name = %q\n", name)
	fmt.Fprintf(&b, "default_env = %q\n\n", envName)
	fmt.Fprintf(&b, "[env.%s]\n", envName)
	fmt.Fprintf(&b, "base_url = %q\n", base.String())
	if specURL != "" {
		fmt.Fprintf(&b, "spec_url = %q\n", specURL)
	}
	if base.Scheme == "https" && config.IsLocalHost(base.Hostname()) {
		b.WriteString("insecure = true  # skip TLS verification, accepted only for localhost\n")
	}
	if authName != "" {
		fmt.Fprintf(&b, "auth = %q  # a profile in ~/.config/blip/credentials.toml\n", authName)
	}
	return b.String()
}

// writeClaudeNote appends to CLAUDE.md rather than rewriting it, and says nothing
// twice.
func (rt *Runtime) writeClaudeNote(root string) error {
	path := filepath.Join(root, "CLAUDE.md")

	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return output.Configf("%s is a symlink; refusing to write through it", path)
	}

	existing, err := os.ReadFile(path)
	switch {
	case err == nil && strings.Contains(string(existing), "blip describe --compact"):
		fmt.Fprintf(rt.Stderr, "blip: %s already documents blip, left alone\n", path)
		return nil
	case err == nil:
		body := string(existing)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if err := os.WriteFile(path, []byte(body+"\n"+claudeNote), 0o644); err != nil {
			return fmt.Errorf("appending to %s: %w", path, err)
		}
		fmt.Fprintf(rt.Stderr, "blip: appended to %s\n", path)
	default:
		if err := writeNew(path, []byte(claudeNote), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(rt.Stderr, "blip: wrote %s\n", path)
	}
	return nil
}

// writePermission creates .claude/settings.json only when there is none. An
// existing one is somebody's configuration, and merging into it silently is a
// good way to break a setting they care about.
func (rt *Runtime) writePermission(root string) error {
	dir := filepath.Join(root, ".claude")
	path := filepath.Join(dir, "settings.json")

	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return output.Configf("%s is a symlink; refusing to write through it", dir)
	}

	if existing, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(existing), PermissionRule) {
			fmt.Fprintf(rt.Stderr, "blip: %s already allows blip\n", path)
			return nil
		}
		fmt.Fprintf(rt.Stderr, "blip: %s exists; add %q to permissions.allow yourself\n", path, PermissionRule)
		return nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	body := fmt.Sprintf("{\n  \"permissions\": {\n    \"allow\": [%q]\n  }\n}\n", PermissionRule)
	if err := writeNew(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(rt.Stderr, "blip: wrote %s\n", path)
	return nil
}

func (rt *Runtime) nextSteps(authName, specURL string) {
	fmt.Fprintln(rt.Stderr, "\nblip: next:")
	if authName != "" {
		fmt.Fprintf(rt.Stderr, "  1. add a [%s] profile to ~/.config/blip/credentials.toml at mode 0600\n", authName)
		fmt.Fprintln(rt.Stderr, "  2. blip auth test")
		fmt.Fprintln(rt.Stderr, "  3. blip describe --compact")
		return
	}
	fmt.Fprintln(rt.Stderr, "  1. blip envs")
	fmt.Fprintln(rt.Stderr, "  2. blip describe --compact")
	if specURL == "" {
		fmt.Fprintln(rt.Stderr, "     (if that reports no spec, set spec_url or declare [[route]] entries)")
	}
}
