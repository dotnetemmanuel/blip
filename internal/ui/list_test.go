package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

// sampleSpec has two groups of two operations each, deliberately small so
// movement, folding and search can be reasoned about by hand. getBeta is
// deprecated to exercise the muted-row rendering rule.
const sampleSpec = `{"openapi":"3.0.1","info":{"title":"Sample","version":"1"},"paths":{
	"/api/alpha":{"get":{"tags":["Alpha"],"operationId":"listAlpha","summary":"List every alpha","responses":{"200":{"description":"OK"}}}},
	"/api/alpha/{id}":{"get":{"tags":["Alpha"],"operationId":"getAlpha","summary":"Fetch one alpha","responses":{"200":{"description":"OK"}}}},
	"/api/beta":{"get":{"tags":["Beta"],"operationId":"listBeta","summary":"List every beta","responses":{"200":{"description":"OK"}}}},
	"/api/beta/{id}":{"get":{"tags":["Beta"],"operationId":"getBeta","summary":"Fetch one beta","deprecated":true,"responses":{"200":{"description":"OK"}}}}}}`

func mustParse(t *testing.T, spec string) *build.API {
	t.Helper()
	api, err := build.Parse([]byte(spec))
	if err != nil {
		t.Fatalf("parsing fixture spec: %v", err)
	}
	return api
}

func sampleList(t *testing.T) listModel {
	t.Helper()
	m := newListModel(theme.Theme{})
	m.SetAPI(mustParse(t, sampleSpec))
	return m
}

func send(m listModel, keys ...tea.KeyMsg) listModel {
	for _, k := range keys {
		m, _ = m.Update(k)
	}
	return m
}

func rune1(r rune) tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var (
	up     = tea.KeyMsg{Type: tea.KeyUp}
	down   = tea.KeyMsg{Type: tea.KeyDown}
	slash  = rune1('/')
	esc    = tea.KeyMsg{Type: tea.KeyEsc}
	tab    = tea.KeyMsg{Type: tea.KeyTab}
	back   = tea.KeyMsg{Type: tea.KeyBackspace}
	space  = tea.KeyMsg{Type: tea.KeySpace}
	nKey   = rune1('n')
	shiftN = rune1('N')
)

// forceTrueColor makes a rendered fragment carry real ANSI colour codes even
// though go test runs without a terminal, then puts the profile back so a
// test file that sorts after this one is never run under a forced profile it
// never asked for.
func forceTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestSetAPISelectsTheFirstOperation(t *testing.T) {
	m := sampleList(t)
	got := m.Selected()
	if got == nil || got.FullName() != "listAlpha" {
		t.Fatalf("Selected() = %v, want listAlpha", got)
	}
}

// The trap: a wrap test that starts in the middle proves nothing, because a
// buggy clamp-instead-of-wrap implementation would also "work" from there.
// Both directions start at the actual end of the list.
func TestMovementWrapsAtBothEnds(t *testing.T) {
	m := sampleList(t)

	// Starting operation is listAlpha, the first row. Up must wrap to the last.
	up1 := send(m, up)
	if got := up1.Selected(); got == nil || got.FullName() != "getBeta" {
		t.Fatalf("up from the first row = %v, want wrap to getBeta", got)
	}

	// Walk to the last row explicitly, then step once more past the end.
	atEnd := send(m, down, down, down)
	if got := atEnd.Selected(); got == nil || got.FullName() != "getBeta" {
		t.Fatalf("three downs from listAlpha = %v, want getBeta", got)
	}
	wrapped := send(atEnd, down)
	if got := wrapped.Selected(); got == nil || got.FullName() != "listAlpha" {
		t.Fatalf("down from the last row = %v, want wrap to listAlpha", got)
	}
}

// Ordinary movement must never stop on a header: stepping past the last
// operation of a group lands straight on the next operation.
func TestMovementSkipsHeadersEntirely(t *testing.T) {
	m := sampleList(t)
	atLastAlpha := send(m, down) // listAlpha -> getAlpha
	if got := atLastAlpha.Selected(); got == nil || got.FullName() != "getAlpha" {
		t.Fatalf("one down from listAlpha = %v, want getAlpha", got)
	}
	next := send(atLastAlpha, down)
	if got := next.Selected(); got == nil || got.FullName() != "listBeta" {
		t.Fatalf("down past the last alpha row = %v, want listBeta directly, not a header stop", got)
	}
}

func TestGroupHopLandsOnTheHeaderNotTheNextRow(t *testing.T) {
	m := sampleList(t)

	hopped := send(m, nKey)
	if got := hopped.Selected(); got != nil {
		t.Fatalf("Selected() after a group hop = %v, want nil: a hop lands on the header, not a row", got)
	}
	if hopped.cursor < 0 || hopped.cursor >= len(hopped.rows) || !hopped.rows[hopped.cursor].header || hopped.rows[hopped.cursor].group != "beta" {
		t.Fatalf("cursor after n = %+v, want the beta header", hopped.rows[hopped.cursor])
	}

	wrapped := send(hopped, nKey)
	if !wrapped.rows[wrapped.cursor].header || wrapped.rows[wrapped.cursor].group != "alpha" {
		t.Fatalf("n from the last group = %+v, want wrap to the alpha header", wrapped.rows[wrapped.cursor])
	}

	hoppedBack := send(wrapped, shiftN)
	if !hoppedBack.rows[hoppedBack.cursor].header || hoppedBack.rows[hoppedBack.cursor].group != "beta" {
		t.Fatalf("N from the first group = %+v, want wrap to the beta header", hoppedBack.rows[hoppedBack.cursor])
	}
}

func TestSearchFiltersOnPathAndSummary(t *testing.T) {
	m := sampleList(t)

	byPath := send(m, slash, runes("/api/beta/"))
	if got := opFullNames(byPath); len(got) != 1 || got[0] != "getBeta" {
		t.Fatalf("filtering on path = %v, want only getBeta", got)
	}

	bySummary := send(m, slash, runes("fetch"))
	if got := opFullNames(bySummary); len(got) != 2 || !contains(got, "getAlpha") || !contains(got, "getBeta") {
		t.Fatalf("filtering on summary = %v, want getAlpha and getBeta only", got)
	}
}

// The trap: an esc-restores test where the pre-search selection is row 0
// passes even if nothing was actually restored. Move first.
func TestEscRestoresTheFullListAndThePriorSelection(t *testing.T) {
	m := sampleList(t)
	m = send(m, down) // select getAlpha, not the first row

	filtered := send(m, slash, runes("beta"))
	if got := opFullNames(filtered); len(got) != 2 {
		t.Fatalf("filtered rows = %v, want the two beta operations", got)
	}
	if got := filtered.Selected(); got == nil || got.FullName() != "listBeta" {
		t.Fatalf("selection while filtered = %v, want the filter to land on the first match", got)
	}

	restored := send(filtered, esc)
	if got := opFullNames(restored); len(got) != 4 {
		t.Fatalf("rows after esc = %v, want all four operations back", got)
	}
	if got := restored.Selected(); got == nil || got.FullName() != "getAlpha" {
		t.Fatalf("selection after esc = %v, want getAlpha restored", got)
	}
}

func TestFoldingHidesItsRowsAndKeepsTheSelectionValid(t *testing.T) {
	m := sampleList(t)
	m = send(m, down) // select getAlpha, inside the group about to fold

	folded := send(m, tab)
	if got := opFullNames(folded); len(got) != 2 || contains(got, "getAlpha") || contains(got, "listAlpha") {
		t.Fatalf("rows after folding alpha = %v, want alpha's operations gone", got)
	}
	if folded.cursor < 0 || folded.cursor >= len(folded.rows) {
		t.Fatalf("cursor %d out of range over %d rows after folding", folded.cursor, len(folded.rows))
	}
	if got := folded.Selected(); got != nil {
		t.Fatalf("Selected() after folding the selected row away = %v, want nil", got)
	}
	if !folded.rows[folded.cursor].header || folded.rows[folded.cursor].group != "alpha" {
		t.Fatalf("cursor after folding = %+v, want it parked on the alpha header", folded.rows[folded.cursor])
	}
	// The header itself stays visible: folding hides the group's rows, not the group.
	if _, ok := folded.indexOfHeader("alpha"); !ok {
		t.Fatal("the alpha header disappeared entirely; folding must keep it")
	}
}

// A search overrides a fold, not the other way around: it exists to help you
// find a row you cannot currently see, so it must be able to reveal one.
func TestUnfoldingRestoresTheRows(t *testing.T) {
	m := sampleList(t)
	folded := send(m, tab)
	unfolded := send(folded, tab)
	if got := opFullNames(unfolded); len(got) != 4 {
		t.Fatalf("rows after unfolding = %v, want all four back", got)
	}
}

func TestSearchOverridesAFoldedGroupAndEscRestoresTheFold(t *testing.T) {
	m := sampleList(t)
	folded := send(m, tab) // selection starts on listAlpha, so tab folds alpha
	if got := opFullNames(folded); contains(got, "listAlpha") {
		t.Fatalf("fixture sanity: rows = %v, want alpha already folded away", got)
	}

	filtered := send(folded, slash, runes("alpha"))
	if got := opFullNames(filtered); len(got) != 2 || !contains(got, "listAlpha") || !contains(got, "getAlpha") {
		t.Fatalf("search while alpha is folded = %v, want alpha's rows back: search overrides fold", got)
	}

	restored := send(filtered, esc)
	if got := opFullNames(restored); contains(got, "listAlpha") {
		t.Fatalf("rows after esc = %v, want alpha still folded: esc restores the query, not the fold state", got)
	}
}

// This is the brief's ambiguity case: every operation on this API carries
// only the .NET assembly tag, which build.Parse discards, so grouping falls
// back to the first path segment and every operation lands in one group.
// The assertion is derived from api.Groups() itself, never a hardcoded count,
// because the point is that listModel reuses build's grouping rather than
// reading tags on its own.
func TestOperationsUnderTheDiscardedAssemblyTagFormOneGroup(t *testing.T) {
	spec := `{"openapi":"3.0.1","info":{"title":"WidgetsApi | v1","version":"1"},"paths":{
		"/api/widgets":{"get":{"tags":["WidgetsApi"],"operationId":"listWidgets","responses":{"200":{"description":"OK"}}}},
		"/api/widgets/{id}":{"get":{"tags":["WidgetsApi"],"operationId":"getWidget","responses":{"200":{"description":"OK"}}}},
		"/api/widgets/{id}/parts":{"get":{"tags":["WidgetsApi"],"operationId":"listWidgetParts","responses":{"200":{"description":"OK"}}}}}}`

	api := mustParse(t, spec)
	if len(api.Groups()) != 1 {
		t.Fatalf("fixture sanity: api.Groups() = %v, want exactly one group so the test proves something", api.Groups())
	}

	m := newListModel(theme.Theme{})
	m.SetAPI(api)

	headers := headerGroups(m)
	if len(headers) != 1 {
		t.Fatalf("group headers rendered = %v, want one, not one per operation", headers)
	}
	if headers[0] != api.Groups()[0] {
		t.Fatalf("header group = %q, want api.Groups()[0] = %q", headers[0], api.Groups()[0])
	}
	if got := opFullNames(m); len(got) != len(api.Operations) {
		t.Fatalf("operation rows = %v, want one row for each of the %d operations", got, len(api.Operations))
	}
}

// Proves reuse rather than reimplementation: the group headers listModel
// renders for a real captured fixture are exactly api.Groups(), in that
// order, and every operation appears under the group InGroup puts it in.
func TestListReflectsBuildsGroupingForARealFixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/dotnet10-minimal.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	api, err := build.Parse(data)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	if len(api.Groups()) < 2 {
		t.Fatalf("fixture sanity: want at least two groups, got %v", api.Groups())
	}

	m := newListModel(theme.Theme{})
	m.SetAPI(api)

	if got := headerGroups(m); !equalStrings(got, api.Groups()) {
		t.Fatalf("header groups = %v, want api.Groups() = %v", got, api.Groups())
	}

	for _, group := range api.Groups() {
		want := map[string]bool{}
		for _, op := range api.InGroup(group) {
			want[op.FullName()] = true
		}
		got := map[string]bool{}
		for _, r := range m.rows {
			if !r.header && r.group == group {
				got[r.op.FullName()] = true
			}
		}
		if len(got) != len(want) {
			t.Fatalf("group %q rows = %v, want %v", group, got, want)
		}
		for name := range want {
			if !got[name] {
				t.Errorf("group %q is missing operation %q", group, name)
			}
		}
	}
}

func TestBackspaceDuringSearchNarrowsTheQuery(t *testing.T) {
	m := sampleList(t)
	typed := send(m, slash, runes("beta"))
	if got := opFullNames(typed); len(got) != 2 {
		t.Fatalf("query beta = %v, want the two beta operations", got)
	}
	trimmed := send(typed, back, back, back, back)
	if got := opFullNames(trimmed); len(got) != 4 {
		t.Fatalf("query emptied by backspace = %v, want every operation back", got)
	}
}

// A phrase containing a space is what a search for a summary actually looks
// like. bubbletea reports a lone space as tea.KeySpace, not as KeyRunes, so a
// query built only from the KeyRunes case silently drops every space typed.
func TestSpaceDuringSearchJoinsTheQueryInsteadOfBeingDropped(t *testing.T) {
	m := sampleList(t)
	typed := send(m, slash, runes("fetch"), space, runes("one"))
	if got := opFullNames(typed); len(got) != 2 || !contains(got, "getAlpha") || !contains(got, "getBeta") {
		t.Fatalf(`query %q rows = %v, want getAlpha and getBeta: a typed space must join the query, not vanish`, typed.query, got)
	}
}

// n, N and a fold all move the cursor onto a header row, and that has to be
// visible or the headline feature of this list (browsing by group) draws
// nothing when used.
func TestGroupHeaderRendersDifferentlyWhenSelected(t *testing.T) {
	forceTrueColor(t)

	th := theme.Theme{Focus: "#123456", FocusBg: "#abcdef"}
	m := listModel{theme: th}
	row := listRow{header: true, group: "alpha"}

	unselected := m.renderRow(row, false)
	selectedRow := m.renderRow(row, true)
	if unselected == selectedRow {
		t.Fatalf("a header renders identically whether or not the cursor is on it: %q", unselected)
	}

	want := styled(th, 0, th.Focus).Background(th.FocusBg).Render("- alpha")
	if selectedRow != want {
		t.Fatalf("selected header = %q, want %q", selectedRow, want)
	}
}

func TestMethodBadgeUsesTheSemanticRole(t *testing.T) {
	forceTrueColor(t)

	th := theme.Theme{Info: "#111111", Success: "#222222", Warning: "#333333", Danger: "#444444", Text: "#555555"}
	m := listModel{theme: th}

	cases := []struct {
		method string
		want   lipgloss.Color
	}{
		{"GET", th.Info},
		{"POST", th.Success},
		{"PUT", th.Warning},
		{"PATCH", th.Warning},
		{"DELETE", th.Danger},
	}
	for _, c := range cases {
		op := &build.Operation{Method: c.method, Path: "/x"}
		got := m.renderRow(listRow{op: op}, false)
		want := lipgloss.NewStyle().Foreground(c.want).Render(padMethod(c.method))
		if !strings.Contains(got, want) {
			t.Errorf("%s badge = %q, want it to contain %q", c.method, got, want)
		}
	}
}

func TestDeprecatedOperationRendersMutedThroughout(t *testing.T) {
	forceTrueColor(t)

	th := theme.Theme{Info: "#111111", Muted: "#999999", Text: "#555555"}
	m := listModel{theme: th}

	op := &build.Operation{Method: "GET", Path: "/x", Deprecated: true}
	got := m.renderRow(listRow{op: op}, false)

	wantBadge := lipgloss.NewStyle().Foreground(th.Muted).Render(padMethod("GET"))
	if !strings.Contains(got, wantBadge) {
		t.Errorf("deprecated GET badge = %q, want Muted (%q), not Info", got, wantBadge)
	}

	infoBadge := lipgloss.NewStyle().Foreground(th.Info).Render(padMethod("GET"))
	if strings.Contains(got, infoBadge) {
		t.Errorf("deprecated GET badge = %q, still carries Info; a deprecated row must never look safe", got)
	}
}

// A listModel built as a bare struct literal (rather than through
// newListModel or SetAPI) has a nil folded map. Task 11 embedding this model
// on the root Model has no reason to know that only two of its constructors
// are safe to use, so toggleFold must tolerate the nil map itself.
func TestToggleFoldOnANilFoldedMapDoesNotPanic(t *testing.T) {
	m := listModel{rows: []listRow{{group: "alpha", op: &build.Operation{ID: "x"}}}}

	m.toggleFold()

	if !m.folded["alpha"] {
		t.Fatalf("folded[%q] = %v, want true after the first toggle", "alpha", m.folded["alpha"])
	}
}

func headerGroups(m listModel) []string {
	var groups []string
	for _, r := range m.rows {
		if r.header {
			groups = append(groups, r.group)
		}
	}
	return groups
}

func opFullNames(m listModel) []string {
	var names []string
	for _, r := range m.rows {
		if !r.header {
			names = append(names, r.op.FullName())
		}
	}
	return names
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
