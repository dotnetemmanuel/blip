package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotnetemmanuel/blip/internal/output"
)

func TestUIRefusesWhenStdoutIsNotATerminal(t *testing.T) {
	f := newFixture(t, twoEnvs)

	got := f.run(t, "ui")

	if got.code != output.ExitUsage {
		t.Fatalf("exit = %d, want %d (%s)", got.code, output.ExitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "terminal") {
		t.Errorf("stderr should say it needs a terminal:\n%s", got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout must stay empty, got %q", got.stdout)
	}
}

// ui discovers its own API, so a repo with no .blip.toml must fail on the missing
// terminal rather than on missing config.
func TestUINeedsNoConfig(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rt := &Runtime{
		Globals: &Globals{},
		Stdout:  stdout,
		Stderr:  stderr,
		Dir:     t.TempDir(),
	}

	if code := Run(rt, []string{"ui"}); code != output.ExitUsage {
		t.Fatalf("exit = %d, want %d (%s)", code, output.ExitUsage, stderr.String())
	}
}

// A pipe on stdin must not be handed to the renderer: it never types, and the
// screen would be drawn with no key able to close it.
func TestUITakesStdinOnlyFromATerminal(t *testing.T) {
	rt := &Runtime{Globals: &Globals{}, Stdin: os.Stdin}

	if got := rt.uiInput(); got != nil {
		t.Errorf("want no reader when stdin is not a terminal, got %T", got)
	}

	rt.StdinIsTTY = true
	if rt.uiInput() == nil {
		t.Error("want the terminal handed through when there is one")
	}
}

func TestUIDiscoversWhereConfigPoints(t *testing.T) {
	elsewhere := t.TempDir()
	rt := &Runtime{
		Globals: &Globals{ConfigPath: filepath.Join(elsewhere, ".blip.toml")},
		Dir:     t.TempDir(),
	}

	got, err := rt.discoveryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != elsewhere {
		t.Errorf("discovery root = %q, want the directory --config names, %q", got, elsewhere)
	}
}

// The ladder looks for its config by name, so a config under another name would
// send ui to a different API from every other command in the same invocation.
func TestUIRefusesAConfigItCannotFind(t *testing.T) {
	rt := &Runtime{Globals: &Globals{ConfigPath: filepath.Join(t.TempDir(), "staging.toml")}}

	_, err := rt.discoveryRoot()
	if err == nil {
		t.Fatal("want a refusal rather than a silent divergence")
	}
	if code := output.ExitCodeFor(err); code != output.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, output.ExitUsage)
	}
	if !strings.Contains(err.Error(), "staging.toml") {
		t.Errorf("want the offending name in the message, got %q", err)
	}
}

func TestTheDefaultThemeHasColors(t *testing.T) {
	th := defaultTheme()

	if th.Primary == "" || th.Text == "" || th.Muted == "" {
		t.Fatalf("the ui would render with no color at all: %+v", th)
	}
}
