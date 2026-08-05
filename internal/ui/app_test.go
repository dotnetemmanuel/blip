package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/detect"
	"github.com/dotnetemmanuel/blip/internal/output"
)

func TestRunRefusesWithoutATerminal(t *testing.T) {
	err := Run(context.Background(), Options{Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("want a refusal when stdout is not a terminal")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("want the error to say it needs a terminal, got %q", err)
	}
	if code := output.ExitCodeFor(err); code != output.ExitUsage {
		t.Errorf("want exit code %d, got %d", output.ExitUsage, code)
	}
}

// The whole program, not just Update: everything between the guard and the model
// is untested otherwise, and that is where a wedged input loop would live.
func TestRunQuitsWhenTheUserPressesQ(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			Stdout:      &bytes.Buffer{},
			Stdin:       strings.NewReader("q"),
			StdoutIsTTY: true,
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("want a clean exit, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run never returned: q did not reach the model")
	}
}

// bubbletea reports a broken terminal by wrapping the cause in ErrProgramKilled,
// so treating that error as a clean quit would report a failure as success.
func TestOnlyADeliberateStopExitsClean(t *testing.T) {
	clean := []error{nil, tea.ErrInterrupted, tea.ErrProgramKilled, fmt.Errorf("%w: %w", tea.ErrProgramKilled, context.Canceled)}
	for _, err := range clean {
		if got := exitError(err); got != nil {
			t.Errorf("exitError(%v) = %v, want a clean exit", err, got)
		}
	}

	broken := fmt.Errorf("%w: %w", tea.ErrProgramKilled, errors.New("read /dev/tty: input/output error"))
	if code := output.ExitCodeFor(exitError(broken)); code != output.ExitInternal {
		t.Errorf("a terminal that died mid-session must exit %d, got %d", output.ExitInternal, code)
	}

	panicked := fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrProgramPanic)
	if code := output.ExitCodeFor(exitError(panicked)); code != output.ExitInternal {
		t.Errorf("a panic must exit %d, got %d", output.ExitInternal, code)
	}
}

// Quitting must not leave the ladder probing ports behind the closed screen.
func TestQuittingCancelsDiscovery(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan error, 1)

	// Typed only once discovery is running: a q already waiting in stdin can quit
	// the program before bubbletea dispatches the Init command.
	keys, typed := io.Pipe()
	defer typed.Close()

	go Run(context.Background(), Options{
		Stdout:      &bytes.Buffer{},
		Stdin:       keys,
		StdoutIsTTY: true,
		Discover: func(ctx context.Context) ([]detect.Target, detect.Ledger, error) {
			close(started)
			<-ctx.Done()
			finished <- ctx.Err()
			return nil, detect.Ledger{}, ctx.Err()
		},
	})

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("discovery never started")
	}
	if _, err := typed.Write([]byte("q")); err != nil {
		t.Fatalf("could not type q: %v", err)
	}

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("want discovery cancelled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quitting left discovery running")
	}
}

// Run must go through exitError, not just have it defined next door.
func TestRunTreatsCancellationAsAQuit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Stdout: &bytes.Buffer{}, Stdin: strings.NewReader(""), StdoutIsTTY: true})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled context is a quit, not a failure: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run never returned on a cancelled context")
	}
}

func TestQuitsOnQ(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
	} {
		m := New(context.Background(), Options{})
		_, cmd := m.Update(key)
		if cmd == nil {
			t.Fatalf("%s: want a command", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: want tea.QuitMsg, got %T", key, cmd())
		}
	}
}

func TestNothingIsStartedWithoutADiscoverer(t *testing.T) {
	if cmd := New(context.Background(), Options{}).Init(); cmd != nil {
		t.Error("want no command when there is nothing to discover")
	}
}

func TestDiscoveryResultReachesTheModel(t *testing.T) {
	want := []detect.Target{{Title: "sandbox", BaseURL: "http://127.0.0.1:5080"}}
	m := New(context.Background(), Options{Discover: func(context.Context) ([]detect.Target, detect.Ledger, error) {
		return want, detect.Ledger{}, nil
	}})

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("want Init to start discovery")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)

	if len(m.targets) != 1 || m.targets[0].Title != "sandbox" {
		t.Fatalf("want the discovered target on the model, got %+v", m.targets)
	}
	if m.discovering {
		t.Error("want discovery to be finished")
	}
}

// Quitting must not leave the ladder probing ports in the background.
func TestDiscoveryRunsUnderTheProgramContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var got error
	m := New(ctx, Options{Discover: func(ctx context.Context) ([]detect.Target, detect.Ledger, error) {
		got = ctx.Err()
		return nil, detect.Ledger{}, nil
	}})
	m.Init()()

	if !errors.Is(got, context.Canceled) {
		t.Errorf("want discovery to see the cancelled context, got %v", got)
	}
}

func TestDiscoveryFailureIsHeldNotSwallowed(t *testing.T) {
	boom := errors.New("malformed .blip.toml")
	m := New(context.Background(), Options{Discover: func(context.Context) ([]detect.Target, detect.Ledger, error) {
		return nil, detect.Ledger{}, boom
	}})

	next, _ := m.Update(m.Init()())
	m = next.(Model)

	if !errors.Is(m.err, boom) {
		t.Fatalf("want the discovery error held on the model, got %v", m.err)
	}
	if !strings.Contains(m.View(), boom.Error()) {
		t.Error("want the failure shown rather than an empty screen")
	}
}

func TestAnUnreachableTargetSaysWhy(t *testing.T) {
	m := New(context.Background(), Options{})
	m.targets = []detect.Target{{Title: "orders", Unsendable: "this spec file names no server"}}

	if !strings.Contains(m.View(), "this spec file names no server") {
		t.Errorf("want the reason on screen instead of a blank:\n%s", m.View())
	}
}

// The renderer chops anything wider than the screen, and the ledger is the one
// thing on this screen the user has to read in full.
func TestNothingIsDrawnWiderThanTheScreen(t *testing.T) {
	const width = 40

	sized, _ := New(context.Background(), Options{}).Update(tea.WindowSizeMsg{Width: width, Height: 20})

	// Each screen separately: a model with targets never renders the ledger.
	empty := sized.(Model)
	empty.ledger.Attempts = []detect.Attempt{{
		Rung:    detect.RungSpecFile,
		Detail:  "looked for openapi.json, openapi.yaml, openapi.yml, swagger.json under ., api, docs, spec",
		Outcome: "none found",
	}}

	found := sized.(Model)
	found.targets = []detect.Target{{Title: "a service with a very long name indeed", Unsendable: "this spec file names no server at all"}}

	failed := sized.(Model)
	failed.err = errors.New("this .blip.toml names an environment that the file itself never declares anywhere")
	failed.ledger = empty.ledger

	// D: the browsing footer is 51 columns unwrapped, well past 40, and it is
	// drawn by every browsing screen regardless of what is selected.
	browsing := sized.(Model)
	browsing.mode = modeBrowsing
	browsing.list.SetAPI(mustParse(t, sampleSpec))
	browsing.detail.SetOperation(browsing.list.Selected())

	choosing := sized.(Model)
	choosing.mode = modeChoosing
	choosing.targets = []detect.Target{
		{Title: "a service with a very long name indeed", BaseURL: "http://localhost:5080"},
		{Title: "another one, also fairly verbose about it"},
	}

	screens := map[string]Model{
		"no API found": empty, "targets": found, "failure": failed,
		"browsing": browsing, "choosing": choosing,
	}
	for name, m := range screens {
		for _, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("%s: line is %d columns on a %d column screen: %q", name, w, width, line)
			}
		}
	}

	// Wrapped, not chopped: fitting the screen by throwing the tail away would
	// satisfy the width check above and lose the half that explains the failure.
	collapsed := strings.Join(strings.Fields(empty.View()), " ")
	if !strings.Contains(collapsed, "under ., api, docs, spec: none found") {
		t.Errorf("the end of the ledger was lost:\n%s", empty.View())
	}
}

func TestTheLedgerExplainsAnEmptyResult(t *testing.T) {
	m := New(context.Background(), Options{})
	m.ledger.Attempts = []detect.Attempt{{Rung: detect.RungConfig, Detail: "looked for .blip.toml", Outcome: "not found"}}

	view := m.View()
	if !strings.Contains(view, "no API found") || !strings.Contains(view, "not found") {
		t.Errorf("want the ledger to explain the empty result:\n%s", view)
	}
}
