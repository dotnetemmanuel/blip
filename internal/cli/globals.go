package cli

import (
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// Globals are the flags that apply to every command.
type Globals struct {
	ConfigPath     string
	Env            string
	Profile        string
	Refresh        bool
	Offline        bool
	Timeout        time.Duration
	Output         string
	IncludeHeaders bool
	Verbose        bool
	DryRun         bool
	Yes            bool
	Strict         bool
}

const (
	OutputJSON   = "json"
	OutputRaw    = "raw"
	OutputStatus = "status"
)

func (g *Globals) register(flags *pflag.FlagSet) {
	flags.StringVar(&g.ConfigPath, "config", g.ConfigPath, "path to .blip.toml (default: nearest one walking up from cwd)")
	flags.StringVar(&g.Env, "env", g.Env, "environment to use (default: BLIP_ENV, then default_env)")
	flags.StringVar(&g.Profile, "profile", g.Profile, "credentials profile, overriding the environment's auth")
	flags.BoolVar(&g.Refresh, "refresh", g.Refresh, "revalidate the cached spec before running")
	flags.BoolVar(&g.Offline, "offline", g.Offline, "never touch the network for the spec; fail if the cache is cold")
	flags.DurationVar(&g.Timeout, "timeout", g.Timeout, "request timeout, overriding the environment's")
	flags.StringVar(&g.Output, "output", OutputJSON, "output mode: json, raw or status")
	flags.BoolVar(&g.IncludeHeaders, "include-headers", g.IncludeHeaders, "write response headers to stderr")
	flags.BoolVarP(&g.Verbose, "verbose", "v", g.Verbose, "explain what is being sent, secrets redacted")
	flags.BoolVar(&g.DryRun, "dry-run", g.DryRun, "print the resolved request and exit without sending")
	flags.BoolVar(&g.Yes, "yes", g.Yes, "confirm a mutating request without prompting")
	flags.BoolVar(&g.Strict, "strict", g.Strict, "fail when a response does not match the spec schema")
}

// prescanned are the flags blip must know before the command tree exists, because
// the tree itself is built from the spec that these select.
var prescanned = map[string]bool{
	"--config":  true,
	"--env":     true,
	"--profile": true,
}

var prescannedBools = map[string]bool{
	"--refresh": true,
	"--offline": true,
}

// prescan reads the config-selecting flags straight out of argv. cobra cannot do
// this for us: the tree it would parse against does not exist yet.
func (g *Globals) prescan(args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return
		}

		name, value, hasValue := strings.Cut(arg, "=")
		if prescannedBools[name] && !hasValue {
			g.set(name, "true")
			continue
		}
		if prescannedBools[name] && hasValue {
			g.set(name, value)
			continue
		}
		if !prescanned[name] {
			continue
		}
		if hasValue {
			g.set(name, value)
			continue
		}
		if i+1 < len(args) {
			g.set(name, args[i+1])
			i++
		}
	}
}

func (g *Globals) set(name, value string) {
	switch name {
	case "--config":
		g.ConfigPath = value
	case "--env":
		g.Env = value
	case "--profile":
		g.Profile = value
	case "--refresh":
		g.Refresh = value != "false"
	case "--offline":
		g.Offline = value != "false"
	}
}
