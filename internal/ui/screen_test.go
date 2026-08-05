package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/detect"
)

var enterKey = tea.KeyMsg{Type: tea.KeyEnter}

func TestSingleTargetLoadsAndShowsFirstOperation(t *testing.T) {
	api := mustParse(t, sampleSpec)
	target := detect.Target{Title: "sample", BaseURL: "http://localhost:1", SpecURL: "http://localhost:1/openapi.json"}
	var requested detect.Target

	m := New(context.Background(), Options{
		Discover: func(context.Context) ([]detect.Target, detect.Ledger, error) {
			return []detect.Target{target}, detect.Ledger{}, nil
		},
		LoadAPI: func(_ context.Context, got detect.Target) (*build.API, error) {
			requested = got
			return api, nil
		},
	})

	next, cmd := m.Update(m.Init()())
	m = next.(Model)
	if m.mode != modeLoading {
		t.Fatalf("mode = %v, want modeLoading right after a single target is discovered", m.mode)
	}
	if cmd == nil {
		t.Fatal("want a command that loads the chosen target's spec")
	}

	next, _ = m.Update(cmd())
	m = next.(Model)

	if requested.Title != "sample" {
		t.Errorf("loadAPI was called with %+v, want the discovered target", requested)
	}
	if m.mode != modeBrowsing {
		t.Fatalf("mode = %v, want modeBrowsing once the spec has loaded", m.mode)
	}
	first := m.list.Selected()
	if first == nil || first.Path != "/api/alpha" {
		t.Fatalf("list selection = %+v, want the first operation /api/alpha", first)
	}
	if !strings.Contains(m.detail.View(), "/api/alpha") {
		t.Errorf("detail pane = %q, want it to show the first operation", m.detail.View())
	}
}

func TestSelectionMovementUpdatesDetailPane(t *testing.T) {
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, sampleSpec))
	m.mode = modeBrowsing
	m.detail.SetOperation(m.list.Selected())

	first := m.list.Selected()
	if first == nil || !strings.Contains(m.detail.View(), first.Path) {
		t.Fatalf("detail pane does not show the first operation %+v:\n%s", first, m.detail.View())
	}

	// Two steps crosses out of the Alpha group entirely: listAlpha, getAlpha,
	// then listBeta, so the path is not merely a longer version of the first.
	next, _ := m.Update(down)
	m = next.(Model)
	next, _ = m.Update(down)
	m = next.(Model)

	second := m.list.Selected()
	if second == nil || second.Path != "/api/beta" {
		t.Fatalf("cursor = %+v, want it on /api/beta after two moves", second)
	}
	if !strings.Contains(m.detail.View(), "/api/beta") {
		t.Errorf("detail pane still shows the old operation, want %q:\n%s", "/api/beta", m.detail.View())
	}
	if strings.Contains(m.detail.View(), "/api/alpha") {
		t.Errorf("detail pane still carries the first operation's path:\n%s", m.detail.View())
	}
}

// Task 10's contract has Selected() return nil on a group header. hopGroup
// (the n key) always lands on a header, which is how this test guarantees the
// cursor is genuinely there rather than merely hoping the first row is one.
func TestHeaderSelectionLeavesDetailPaneSensibleRatherThanPanicking(t *testing.T) {
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, sampleSpec))
	m.mode = modeBrowsing
	m.detail.SetOperation(m.list.Selected())

	next, _ := m.Update(nKey)
	m = next.(Model)

	if got := m.list.Selected(); got != nil {
		t.Fatalf("want the cursor on a header (Selected == nil) to set this test up, got %+v", got)
	}
	view := m.detail.View()
	if strings.TrimSpace(view) == "" {
		t.Fatal("detail pane is blank with the cursor on a header")
	}
	if !strings.Contains(view, "select an operation") {
		t.Errorf("detail pane = %q, want the no-selection placeholder", view)
	}
}

func TestSpecLoadFailureShowsReasonAndLedgerWithoutExiting(t *testing.T) {
	boom := errors.New("401 unauthorized fetching the spec")
	target := detect.Target{Title: "sample", SpecURL: "http://localhost:1/openapi.json"}
	ledger := detect.Ledger{Attempts: []detect.Attempt{
		{Rung: detect.RungConfig, Detail: ".blip.toml at /repo/.blip.toml", Outcome: "found, used directly"},
	}}

	m := New(context.Background(), Options{
		Discover: func(context.Context) ([]detect.Target, detect.Ledger, error) {
			return []detect.Target{target}, ledger, nil
		},
		LoadAPI: func(context.Context, detect.Target) (*build.API, error) {
			return nil, boom
		},
	})

	next, cmd := m.Update(m.Init()())
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	if m.mode != modeFailed {
		t.Fatalf("mode = %v, want modeFailed", m.mode)
	}
	view := m.View()
	if !strings.Contains(view, boom.Error()) {
		t.Errorf("view does not carry the load failure:\n%s", view)
	}
	if !strings.Contains(view, "found, used directly") {
		t.Errorf("view does not carry the discovery ledger alongside the failure:\n%s", view)
	}

	_, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if quitCmd == nil {
		t.Fatal("want q to still quit after a load failure; the program must not have exited on its own")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Errorf("want a tea.QuitMsg, got %T", quitCmd())
	}
}

func TestSeveralTargetsAreOfferedAsAChoiceAndChoosingOneLoadsIt(t *testing.T) {
	api := mustParse(t, sampleSpec)
	targets := []detect.Target{
		{Title: "first", SpecURL: "http://localhost:1/a.json"},
		{Title: "second", SpecURL: "http://localhost:1/b.json"},
	}
	var requested detect.Target

	m := New(context.Background(), Options{
		Discover: func(context.Context) ([]detect.Target, detect.Ledger, error) {
			return targets, detect.Ledger{}, nil
		},
		LoadAPI: func(_ context.Context, got detect.Target) (*build.API, error) {
			requested = got
			return api, nil
		},
	})

	next, _ := m.Update(m.Init()())
	m = next.(Model)
	if m.mode != modeChoosing {
		t.Fatalf("mode = %v, want modeChoosing with several targets", m.mode)
	}
	if !strings.Contains(m.View(), "first") || !strings.Contains(m.View(), "second") {
		t.Errorf("view = %q, want both targets offered as a choice", m.View())
	}

	next, _ = m.Update(down)
	m = next.(Model)
	next, cmd := m.Update(enterKey)
	m = next.(Model)

	if m.mode != modeLoading {
		t.Fatalf("mode = %v, want modeLoading once a target is chosen", m.mode)
	}
	if cmd == nil {
		t.Fatal("want a command that loads the chosen target")
	}
	cmd()

	if requested.Title != "second" {
		t.Fatalf("loadAPI was called with %q, want %q: moving down before enter must change which target loads", requested.Title, "second")
	}
}

func TestQQuitsFromEveryState(t *testing.T) {
	base := New(context.Background(), Options{})
	base.list.SetAPI(mustParse(t, sampleSpec))

	states := map[string]func(*Model){
		"targets listing": func(m *Model) { m.targets = []detect.Target{{Title: "x"}} },
		"choosing":        func(m *Model) { m.mode = modeChoosing; m.targets = []detect.Target{{Title: "x"}, {Title: "y"}} },
		"loading":         func(m *Model) { m.mode = modeLoading },
		"browsing":        func(m *Model) { m.mode = modeBrowsing },
		"failed":          func(m *Model) { m.mode = modeFailed; m.loadErr = errors.New("boom") },
	}

	for name, setup := range states {
		m := base
		setup(&m)

		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		if cmd == nil {
			t.Fatalf("%s: want q to quit", name)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: want tea.QuitMsg, got %T", name, cmd())
		}
	}
}

func TestNilLoadAPIIsToleratedNotPanicked(t *testing.T) {
	m := New(context.Background(), Options{Discover: func(context.Context) ([]detect.Target, detect.Ledger, error) {
		return []detect.Target{{Title: "sample"}}, detect.Ledger{}, nil
	}})

	next, cmd := m.Update(m.Init()())
	m = next.(Model)

	if cmd != nil {
		t.Error("want no load command when Options.LoadAPI is nil")
	}
	if m.mode == modeLoading || m.mode == modeBrowsing {
		t.Errorf("mode = %v, want no browsing attempted without a loader", m.mode)
	}
	_ = m.View()
}

func TestSplitWidthsNeverGoNegative(t *testing.T) {
	for _, width := range []int{-5, -1, 0, 1, 2, 3, 5, 10, 40, 200} {
		left, right := splitWidths(width)
		if left < 0 || right < 0 {
			t.Errorf("splitWidths(%d) = (%d, %d), want both non-negative", width, left, right)
		}
	}
}

// A window too narrow for a sane split must still show something from each
// pane, not silently starve one of them down to nothing.
func TestNarrowWindowStillShowsBothPanes(t *testing.T) {
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, sampleSpec))
	m.mode = modeBrowsing
	m.targets = []detect.Target{{Title: "sample"}}
	m.detail.SetOperation(m.list.Selected())

	next, _ := m.Update(tea.WindowSizeMsg{Width: 10, Height: 5})
	m = next.(Model)

	if m.list.width < 0 || m.detail.width < 0 {
		t.Fatalf("pane widths went negative: list=%d detail=%d", m.list.width, m.detail.width)
	}

	view := stripANSI(m.View())
	if !strings.Contains(view, "beta") {
		t.Errorf("list pane content is missing from a narrow render, the list pane was starved:\n%s", view)
	}
	if !strings.Contains(view, "alpha") {
		t.Errorf("detail pane content is missing from a narrow render, the detail pane was starved:\n%s", view)
	}
}
