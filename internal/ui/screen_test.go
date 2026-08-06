package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
// pane, not silently starve one of them down to nothing. Checked against each
// pane's own View() separately: checking the joined string only proves the
// substring is somewhere in the screen, and at some widths that is satisfiable
// by the list pane alone (its rows carry the same path text the detail
// header does), which would pass even if the detail pane were empty.
func TestNarrowWindowStillShowsBothPanes(t *testing.T) {
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, sampleSpec))
	m.mode = modeBrowsing
	m.targets = []detect.Target{{Title: "sample"}}
	m.detail.SetOperation(m.list.Selected())

	// Height generous enough that this test is only about width; E covers
	// height separately.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m = next.(Model)

	if m.list.width < 0 || m.detail.width < 0 {
		t.Fatalf("pane widths went negative: list=%d detail=%d", m.list.width, m.detail.width)
	}

	listView := stripANSI(m.list.View())
	detailView := stripANSI(m.detail.View())
	if !strings.Contains(listView, "beta") {
		t.Errorf("list pane content is missing from a narrow render, the list pane was starved:\n%s", listView)
	}
	if !strings.Contains(detailView, "alpha") {
		t.Errorf("detail pane content is missing from a narrow render, the detail pane was starved:\n%s", detailView)
	}
}

// browsingModel is a Model already past discovery and loading, cursor on the
// first operation of sampleSpec, ready to receive key messages directly.
func browsingModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, sampleSpec))
	m.mode = modeBrowsing
	m.targets = []detect.Target{{Title: "sample"}}
	m.detail.SetOperation(m.list.Selected())
	return m
}

// C, ruled by the human: while a pane is reading free text, typing wins. "q"
// must type a letter into the search box rather than quit the program.
func TestQTypesIntoTheSearchBoxInsteadOfQuitting(t *testing.T) {
	m := browsingModel(t)

	next, _ := m.Update(slash) // enters search
	m = next.(Model)
	if !m.list.searching {
		t.Fatal("test setup: / did not enter search")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)

	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q quit the program while the search box was focused; it should have typed a letter")
		}
	}
	if m.list.query != "q" {
		t.Errorf("list.query = %q, want %q: q must be typed into the search box", m.list.query, "q")
	}
}

// C: everywhere else, q still quits.
func TestQStillQuitsWhileBrowsingAndNotSearching(t *testing.T) {
	m := browsingModel(t)
	if m.list.searching {
		t.Fatal("test setup: should not be searching")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("want q to quit while browsing and not searching")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("want tea.QuitMsg, got %T", cmd())
	}
}

// C, ruled by the human: ctrl+c quits unconditionally, in every state,
// even out of a text-entry state where a plain q would type instead.
func TestCtrlCQuitsEvenWhileSearching(t *testing.T) {
	m := browsingModel(t)
	next, _ := m.Update(slash)
	m = next.(Model)
	if !m.list.searching {
		t.Fatal("test setup: / did not enter search")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("want ctrl+c to quit even while searching")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("want tea.QuitMsg, got %T", cmd())
	}
}

// E: a window much shorter than the operation list must still keep the
// selected row on screen as the cursor moves through the whole stack, not
// just inside listModel in isolation.
func TestShortWindowKeepsSelectionVisibleWhileBrowsing(t *testing.T) {
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, manyOpsSpec(30)))
	m.mode = modeBrowsing
	m.targets = []detect.Target{{Title: "many"}}
	m.detail.SetOperation(m.list.Selected())

	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	m = next.(Model)

	for i := 0; i < 25; i++ {
		next, _ = m.Update(down)
		m = next.(Model)
	}

	start, end := m.list.visibleWindow()
	if m.list.cursor < start || m.list.cursor >= end {
		t.Fatalf("cursor %d not inside the visible window [%d,%d) after scrolling through a tall list in a short window", m.list.cursor, start, end)
	}
}

// A stale spec (served from the cache because the backend could not be
// reached) must say so on the model and in the browsing view, not resolve to
// an ordinary-looking loaded state.
func TestStaleSpecIsFlaggedOnTheModelAndInTheBrowsingView(t *testing.T) {
	api := mustParse(t, sampleSpec)
	api.Stale = true
	api.LoadNotes = []string{"could not refresh the spec, using the cache from 2020-01-01T00:00:00Z"}
	target := detect.Target{Title: "sample"}

	m := New(context.Background(), Options{
		Discover: func(context.Context) ([]detect.Target, detect.Ledger, error) {
			return []detect.Target{target}, detect.Ledger{}, nil
		},
		LoadAPI: func(context.Context, detect.Target) (*build.API, error) {
			return api, nil
		},
	})

	next, cmd := m.Update(m.Init()())
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	if !m.stale {
		t.Fatal("want m.stale set once a Stale api loads")
	}
	view := m.View()
	if !strings.Contains(view, "could not refresh the spec, using the cache from") {
		t.Errorf("view does not carry the stale warning:\n%s", view)
	}
}

// browsingModelSized is browsingModel resized to width x height, list filled
// with n operations so it has enough rows to actually fill (and, before this
// fix, overflow) the window.
func browsingModelSized(t *testing.T, n, width, height int) Model {
	t.Helper()
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, manyOpsSpec(n)))
	m.mode = modeBrowsing
	m.targets = []detect.Target{{Title: "many"}}
	m.detail.SetOperation(m.list.Selected())
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(Model)
}

// The whole screen, title included, must fit inside the height it was given:
// listModel.View ending every row with its own trailing newline made
// lipgloss.JoinHorizontal count one extra (blank) row beyond what was
// actually visible, so a full list pane rendered one line taller than
// SetHeight asked for and pushed the title off the top.
func TestBrowsingScreenFitsExactlyWithinTheGivenHeight(t *testing.T) {
	const height = 20
	m := browsingModelSized(t, 30, 80, height)

	if got := lipgloss.Height(m.View()); got > height {
		t.Errorf("View() is %d lines tall on a %d line screen:\n%s", got, height, m.View())
	}
}

// A proxy for what bubbletea itself does: it keeps only the last `height`
// lines of whatever View() draws. If View() is taller than that, the first
// line kept will not be the title.
func TestBrowsingScreenKeepsTheTitleOnScreen(t *testing.T) {
	const height = 20
	m := browsingModelSized(t, 30, 80, height)

	lines := strings.Split(m.View(), "\n")
	visible := lines
	if len(lines) > height {
		visible = lines[len(lines)-height:]
	}
	if len(visible) == 0 || !strings.Contains(visible[0], "blip") {
		t.Errorf("the title scrolled off the top of a %d line screen: first kept line is %q", height, visible[0])
	}
}

// The banner adds lines above the panes; those lines must be counted too, or
// a browsing screen that is also stale overflows by the banner's own height.
func TestBrowsingScreenWithABannerStillFitsTheHeight(t *testing.T) {
	const height = 20
	m := browsingModelSized(t, 30, 80, height)
	m.stale = true
	m.loadNotes = []string{"could not refresh the spec, using the cache from 2020-01-01T00:00:00Z"}
	m.applyLayout()

	if got := lipgloss.Height(m.View()); got > height {
		t.Errorf("View() is %d lines tall on a %d line screen with a banner:\n%s", got, height, m.View())
	}
}

// The search box adds two more lines to the list pane while it is open;
// those must be counted too.
func TestBrowsingScreenWhileSearchingStillFitsTheHeight(t *testing.T) {
	const height = 20
	m := browsingModelSized(t, 30, 80, height)

	next, _ := m.Update(slash)
	m = next.(Model)
	if !m.list.searching {
		t.Fatal("test setup: / did not enter search")
	}

	if got := lipgloss.Height(m.View()); got > height {
		t.Errorf("View() is %d lines tall on a %d line screen while searching:\n%s", got, height, m.View())
	}
}

// browsingModel starts on the first operation of the sample spec, so enter has
// something to fill in.
func TestEnterOpensTheFillPaneForTheSelectedOperation(t *testing.T) {
	m := browsingModel(t)
	if m.list.Selected() == nil {
		t.Fatal("test setup: nothing is selected")
	}

	next, _ := m.Update(enterKey)
	m = next.(Model)

	if m.pane != stateFill {
		t.Fatalf("pane = %q, want the fill pane open", m.pane)
	}
	if m.form.op != m.list.Selected() {
		t.Error("the fill pane was opened on a different operation than the one selected")
	}
}

// A group header is selectable, which is what n means, but there is nothing
// behind it to send.
func TestEnterOnAGroupHeaderOpensNothing(t *testing.T) {
	m := browsingModel(t)
	next, _ := m.Update(nKey)
	m = next.(Model)
	if m.list.Selected() != nil {
		t.Fatal("test setup: n did not land on a group header")
	}

	next, _ = m.Update(enterKey)
	m = next.(Model)

	if m.pane == stateFill {
		t.Error("enter on a group header opened the fill pane with no operation behind it")
	}
}

func TestEscClosesTheFillPane(t *testing.T) {
	m := browsingModel(t)
	next, _ := m.Update(enterKey)
	m = next.(Model)

	next, _ = m.Update(esc)
	m = next.(Model)

	if m.pane == stateFill {
		t.Error("esc left the fill pane open")
	}
}

// C, ruled by the human, and the same rule the search box gets: while a field is
// taking text, q types a letter.
func TestQTypesIntoAFormFieldInsteadOfQuitting(t *testing.T) {
	m := fillableModel(t)
	next, _ := m.Update(enterKey) // opens the fill pane
	m = next.(Model)
	next, _ = m.Update(enterKey) // starts editing the first field
	m = next.(Model)
	if !m.form.Editing() {
		t.Fatal("test setup: the first field did not start taking text")
	}

	next, cmd := m.Update(rune1('q'))
	m = next.(Model)

	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q quit the program while a field was taking text; it should have typed a letter")
		}
	}
	if !strings.Contains(strings.Join(valuesOf(m.form), ""), "q") {
		t.Errorf("form values = %v, want q typed into the focused field", m.form.Values())
	}
}

// fillableSpec declares its parameters, which sampleSpec deliberately does not,
// so the fill pane has something to put a cursor on.
const fillableSpec = `{"openapi":"3.0.1","info":{"title":"Fillable","version":"1"},"paths":{
	"/api/things/{id}":{"get":{"tags":["Things"],"operationId":"getThing",
	 "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
	 "responses":{"200":{"description":"OK"}}}}}}`

func fillableModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), Options{})
	m.list.SetAPI(mustParse(t, fillableSpec))
	m.mode = modeBrowsing
	m.targets = []detect.Target{{Title: "fillable"}}
	m.detail.SetOperation(m.list.Selected())
	return m
}

func valuesOf(m formModel) []string {
	out := make([]string, 0, len(m.fields))
	for _, f := range m.fields {
		out = append(out, f.value)
	}
	return out
}

// The pane swap must not change how tall the screen draws.
func TestBrowsingScreenWithTheFillPaneStillFitsTheHeight(t *testing.T) {
	const height = 20
	m := browsingModelSized(t, 30, 80, height)

	next, _ := m.Update(enterKey)
	m = next.(Model)
	if m.pane != stateFill {
		t.Fatal("test setup: enter did not open the fill pane")
	}

	if got := lipgloss.Height(m.View()); got > height {
		t.Errorf("View() is %d lines tall on a %d line screen with the fill pane open:\n%s", got, height, m.View())
	}
}

// An operation with nothing to fill in must not be offered keys that do nothing.
func TestFillFooterDoesNotOfferEditingWithNoFields(t *testing.T) {
	m := browsingModel(t)
	next, _ := m.Update(enterKey)
	m = next.(Model)
	if len(m.form.fields) != 0 {
		t.Fatal("test setup: the sample operation declares no parameters, so the form should be empty")
	}

	if got := m.footer(); strings.Contains(got, "enter edit") {
		t.Errorf("footer = %q, want it not to offer editing when there is nothing to edit", got)
	}
}

// Search is the fast way to reach an operation in a large API, so enter has to
// be a way through it rather than a dead end that quietly picks something else.
func TestEnterFromSearchOpensTheOperationTheSearchFound(t *testing.T) {
	m := browsingModel(t)
	next, _ := m.Update(slash)
	m = next.(Model)
	next, _ = m.Update(runes("beta/"))
	m = next.(Model)

	found := m.list.Selected()
	if found == nil || found.FullName() != "getBeta" {
		t.Fatalf("test setup: search selected %v, want getBeta", found)
	}

	next, _ = m.Update(enterKey)
	m = next.(Model)

	if m.pane != stateFill {
		t.Fatal("enter while searching opened nothing")
	}
	if m.form.op != found {
		t.Errorf("the fill pane opened on %v, want the operation the search found", m.form.op)
	}
	if m.list.searching {
		t.Error("the search box is still open behind the fill pane")
	}
	if got := m.list.Selected(); got != found {
		t.Errorf("the list selection fell back to %v, want it to stay on the operation just picked", got)
	}
}

// A query reaches inside a folded group, so committing it has to unfold, or the
// row the cursor was on stops existing and the selection falls somewhere else.
func TestEnterFromSearchInsideAFoldedGroupKeepsEverythingInStep(t *testing.T) {
	m := browsingModel(t)
	next, _ := m.Update(nKey) // onto the beta header
	m = next.(Model)
	next, _ = m.Update(tab) // fold beta
	m = next.(Model)
	if !m.list.folded["beta"] {
		t.Fatal("test setup: tab did not fold the beta group")
	}

	next, _ = m.Update(slash)
	m = next.(Model)
	next, _ = m.Update(runes("beta/"))
	m = next.(Model)
	found := m.list.Selected()
	if found == nil || found.FullName() != "getBeta" {
		t.Fatalf("test setup: search inside a folded group selected %v, want getBeta", found)
	}

	next, _ = m.Update(enterKey)
	m = next.(Model)

	if got := m.list.Selected(); got != found {
		t.Errorf("the list selection is %v, want the operation the search found", got)
	}
	if !strings.Contains(m.detail.View(), found.Path) {
		t.Errorf("the detail pane describes something else:\n%s", m.detail.View())
	}

	// Closing the form must not leave the highlighted row and the pane beside it
	// describing two different operations.
	next, _ = m.Update(esc)
	m = next.(Model)
	if got := m.list.Selected(); got != found || !strings.Contains(m.detail.View(), found.Path) {
		t.Errorf("after esc the list is on %v and the pane shows:\n%s", got, m.detail.View())
	}
}
