package ui

import (
	"net/http"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

// listRow is one visible line: either a group header or one operation. Grouping
// itself is never computed here; it comes from build.API.Groups and InGroup, so
// the tag rule (including discarding .NET's assembly tag) lives in one place.
type listRow struct {
	group  string
	header bool
	op     *build.Operation
}

// listModel browses a parsed API: grouped, foldable rows with search and
// group-to-group hopping. It knows nothing about the terminal beyond its width;
// everything happens by message, per the rest of this package.
type listModel struct {
	theme theme.Theme
	width int

	api    *build.API
	folded map[string]bool

	rows   []listRow
	cursor int

	searching  bool
	query      string
	restoreSel string
}

func newListModel(t theme.Theme) listModel {
	return listModel{theme: t, folded: map[string]bool{}}
}

// SetAPI loads a freshly parsed API: every group starts unfolded and the first
// operation is selected.
func (m *listModel) SetAPI(api *build.API) {
	m.api = api
	m.folded = map[string]bool{}
	m.searching = false
	m.query = ""
	m.restoreSel = ""
	m.rebuild()
	m.cursor = m.firstOpIndex()
}

// SetWidth records the screen width rows should wrap to.
func (m *listModel) SetWidth(width int) {
	m.width = width
}

// Selected is the operation the cursor rests on. It is nil when the cursor
// rests on a group header, which only happens right after a group hop or a
// fold that hid the operation that used to be selected.
func (m *listModel) Selected() *build.Operation {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].op
}

// Update handles movement, group folding, search and group hopping. It never
// draws; every case here is provable by sending a tea.KeyMsg and inspecting
// the returned model.
func (m listModel) Update(msg tea.Msg) (listModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.searching {
		switch key.Type {
		case tea.KeyEsc:
			m.exitSearch()
		case tea.KeyBackspace:
			if r := []rune(m.query); len(r) > 0 {
				m.setQuery(string(r[:len(r)-1]))
			}
		case tea.KeyRunes:
			m.setQuery(m.query + string(key.Runes))
		case tea.KeySpace:
			m.setQuery(m.query + " ")
		case tea.KeyUp:
			m.moveUp()
		case tea.KeyDown:
			m.moveDown()
		}
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		m.moveUp()
	case "down", "j":
		m.moveDown()
	case "n":
		m.hopGroup(1)
	case "N":
		m.hopGroup(-1)
	case "/":
		m.enterSearch()
	case "tab":
		m.toggleFold()
	}
	return m, nil
}

// rebuild recomputes the visible rows from the api, the fold state and the
// search query. A group with no match while searching is left out entirely
// rather than shown with an empty body.
func (m *listModel) rebuild() {
	m.rows = nil
	if m.api == nil {
		return
	}
	query := strings.ToLower(strings.TrimSpace(m.query))

	for _, group := range m.api.Groups() {
		ops := m.api.InGroup(group)
		if query != "" {
			ops = matchingOps(ops, query)
			if len(ops) == 0 {
				continue
			}
		}
		m.rows = append(m.rows, listRow{group: group, header: true})
		if query != "" || !m.folded[group] {
			for _, op := range ops {
				m.rows = append(m.rows, listRow{group: group, op: op})
			}
		}
	}
}

func matchingOps(ops []*build.Operation, query string) []*build.Operation {
	var out []*build.Operation
	for _, op := range ops {
		if strings.Contains(strings.ToLower(op.Path), query) ||
			strings.Contains(strings.ToLower(op.Summary), query) {
			out = append(out, op)
		}
	}
	return out
}

// opIndices lists, in order, the row indices that hold an operation rather
// than a group header.
func (m *listModel) opIndices() []int {
	var idx []int
	for i, r := range m.rows {
		if !r.header {
			idx = append(idx, i)
		}
	}
	return idx
}

func (m *listModel) firstOpIndex() int {
	idx := m.opIndices()
	if len(idx) == 0 {
		return 0
	}
	return idx[0]
}

// moveDown and moveUp step across operation rows only, wrapping at both ends.
// A header row is never a stop: stepping past one from an operation lands
// straight on the next operation, which is what keeps a group hop (which does
// stop on a header) visibly different from ordinary movement.
func (m *listModel) moveDown() {
	m.step(1)
}

func (m *listModel) moveUp() {
	m.step(-1)
}

func (m *listModel) step(delta int) {
	idx := m.opIndices()
	if len(idx) == 0 {
		return
	}
	if pos, ok := indexOf(idx, m.cursor); ok {
		m.cursor = idx[(pos+delta+len(idx))%len(idx)]
		return
	}
	// The cursor is on a header (after a hop or a fold). Land on the nearest
	// operation in the direction of travel, wrapping past either end of the list.
	if delta > 0 {
		for _, v := range idx {
			if v > m.cursor {
				m.cursor = v
				return
			}
		}
		m.cursor = idx[0]
		return
	}
	for i := len(idx) - 1; i >= 0; i-- {
		if idx[i] < m.cursor {
			m.cursor = idx[i]
			return
		}
	}
	m.cursor = idx[len(idx)-1]
}

func indexOf(idx []int, v int) (int, bool) {
	for i, x := range idx {
		if x == v {
			return i, true
		}
	}
	return 0, false
}

// headerIndices lists the row index of every group header currently shown.
func (m *listModel) headerIndices() []int {
	var idx []int
	for i, r := range m.rows {
		if r.header {
			idx = append(idx, i)
		}
	}
	return idx
}

func (m *listModel) currentGroup() string {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	return m.rows[m.cursor].group
}

// hopGroup moves the cursor to the header of the next or previous group,
// wrapping at either end. It always lands on the header itself, never on that
// group's first row, which is what makes it a distinct move from stepping.
func (m *listModel) hopGroup(delta int) {
	idx := m.headerIndices()
	if len(idx) == 0 {
		return
	}
	group := m.currentGroup()
	pos := 0
	for i, h := range idx {
		if m.rows[h].group == group {
			pos = i
			break
		}
	}
	m.cursor = idx[(pos+delta+len(idx))%len(idx)]
}

// toggleFold folds or unfolds the group the selection is currently in. If
// folding hides the selected operation, the cursor moves to that group's
// header, which is the closest thing left to "still selected".
func (m *listModel) toggleFold() {
	group := m.currentGroup()
	if group == "" {
		return
	}
	selected := m.Selected()
	if m.folded == nil {
		m.folded = map[string]bool{}
	}
	m.folded[group] = !m.folded[group]
	m.rebuild()

	if selected != nil {
		if i, ok := m.indexOfOp(selected); ok {
			m.cursor = i
			return
		}
	}
	if i, ok := m.indexOfHeader(group); ok {
		m.cursor = i
		return
	}
	m.cursor = m.firstOpIndex()
}

// enterSearch remembers the current selection so esc can restore it, then
// starts an empty query.
func (m *listModel) enterSearch() {
	if op := m.Selected(); op != nil {
		m.restoreSel = op.FullName()
	} else {
		m.restoreSel = ""
	}
	m.searching = true
	m.query = ""
	m.rebuild()
	m.cursor = m.firstOpIndex()
}

// exitSearch drops the query and restores the selection that was live when
// search was entered, if it is still around to restore.
func (m *listModel) exitSearch() {
	m.searching = false
	m.query = ""
	m.rebuild()
	if m.restoreSel != "" {
		if i, ok := m.indexOfFullName(m.restoreSel); ok {
			m.cursor = i
			return
		}
	}
	m.cursor = m.firstOpIndex()
}

// setQuery reruns the filter and tries to keep the same operation selected;
// if the new query has filtered it out, selection falls to the first match.
func (m *listModel) setQuery(q string) {
	m.query = q
	prev := m.Selected()
	m.rebuild()
	if prev != nil {
		if i, ok := m.indexOfOp(prev); ok {
			m.cursor = i
			return
		}
	}
	m.cursor = m.firstOpIndex()
}

func (m *listModel) indexOfOp(op *build.Operation) (int, bool) {
	for i, r := range m.rows {
		if r.op == op {
			return i, true
		}
	}
	return 0, false
}

func (m *listModel) indexOfHeader(group string) (int, bool) {
	for i, r := range m.rows {
		if r.header && r.group == group {
			return i, true
		}
	}
	return 0, false
}

func (m *listModel) indexOfFullName(name string) (int, bool) {
	for i, r := range m.rows {
		if r.op != nil && r.op.FullName() == name {
			return i, true
		}
	}
	return 0, false
}

// View renders every visible row: a fold marker and name for a header, a
// method badge and path for an operation. A deprecated operation renders
// entirely in Muted, badge included, so it never reads as safe to call.
func (m listModel) View() string {
	var b strings.Builder
	for i, r := range m.rows {
		b.WriteString(m.renderRow(r, i == m.cursor))
		b.WriteString("\n")
	}
	if m.searching {
		b.WriteString("\n/" + m.query)
	}
	return b.String()
}

func (m listModel) renderRow(r listRow, selected bool) string {
	if r.header {
		marker := "-"
		if m.folded[r.group] {
			marker = "+"
		}
		style := styled(m.theme, m.width, m.theme.Focus)
		if selected {
			style = style.Background(m.theme.FocusBg)
		}
		return style.Render(marker + " " + r.group)
	}

	op := r.op
	badgeColor := methodColor(m.theme, op.Method)
	textColor := m.theme.Text
	if op.Deprecated {
		badgeColor = m.theme.Muted
		textColor = m.theme.Muted
	}

	badge := lipgloss.NewStyle().Foreground(badgeColor).Render(padMethod(op.Method))
	line := badge + " " + op.Path

	style := styled(m.theme, m.width, textColor)
	if selected {
		style = style.Background(m.theme.FocusBg)
	}
	return style.Render(line)
}

func padMethod(method string) string {
	const width = 6
	if len(method) >= width {
		return method
	}
	return method + strings.Repeat(" ", width-len(method))
}

// methodColor maps a method onto the semantic role that names its danger: a
// read is Info, a create is Success, an update is Warning, a delete is Danger.
func methodColor(t theme.Theme, method string) lipgloss.Color {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		return t.Info
	case http.MethodPost:
		return t.Success
	case http.MethodPut, http.MethodPatch:
		return t.Warning
	case http.MethodDelete:
		return t.Danger
	default:
		return t.Text
	}
}
