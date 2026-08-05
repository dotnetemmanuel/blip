package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// uiMode is which screen the root model is showing once discovery has an
// answer. The zero value, modeTargets, is the plain reachability listing that
// existed before this file: a Model whose fields are set directly rather than
// driven through Update (as several tests in app_test.go do) lands here, which
// keeps that older behavior intact without every test having to know about mode.
type uiMode int

const (
	modeTargets uiMode = iota
	modeChoosing
	modeLoading
	modeBrowsing
	modeFailed
)

// loadCmd starts loading the chosen target's spec. ctx bounds it, so quitting
// mid-load does not leave a fetch running behind the closed screen. Both call
// sites already guard on m.loadAPI != nil, but the check stays here too since
// a nil LoadAPIFunc must never be dialled, guard or no guard upstream.
func (m Model) loadCmd() tea.Cmd {
	if m.loadAPI == nil {
		return nil
	}
	ctx, loadAPI, target := m.ctx, m.loadAPI, m.chosen
	return func() tea.Msg {
		api, err := loadAPI(ctx, target)
		return loadedMsg{api: api, err: err}
	}
}

// updateChoosing handles the target picker: move the cursor, or load the
// target it rests on.
func (m *Model) updateChoosing(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		m.choiceCursor = wrapIndex(m.choiceCursor-1, len(m.targets))
	case "down", "j":
		m.choiceCursor = wrapIndex(m.choiceCursor+1, len(m.targets))
	case "enter":
		if m.choiceCursor < 0 || m.choiceCursor >= len(m.targets) {
			return nil
		}
		m.chosen = m.targets[m.choiceCursor]
		m.mode = modeLoading
		return m.loadCmd()
	}
	return nil
}

// updateBrowsing forwards the key to the list, then keeps the detail pane in
// step with whatever it now has selected, nil included.
func (m *Model) updateBrowsing(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.detail.SetOperation(m.list.Selected())
	return cmd
}

// textEntryActive reports whether the focused pane is reading free text right
// now, so that a plain "q" types a letter there instead of quitting. Only
// ctrl+c is guaranteed to quit unconditionally. Task 12's Fill pane will add
// another case here.
func (m Model) textEntryActive() bool {
	return m.mode == modeBrowsing && m.list.searching
}

// chromeLines is how many lines View draws around the panes: the title, a
// blank line, then (after the panes) another blank line and the footer.
const chromeLines = 4

// applyLayout splits the screen between the list and the detail pane, and
// gives the list the height it has left to scroll within. Called once per
// resize and once when a spec finishes loading, never per frame: detailModel
// caches its rendered markdown against width, and feeding it a fresh value
// every View() would defeat that cache.
func (m *Model) applyLayout() {
	left, right := splitWidths(m.width)
	m.list.SetWidth(left)
	m.detail.SetWidth(right)
	m.list.SetHeight(contentHeight(m.height))
}

// contentHeight is how many rows the list can show once the chrome around it
// is accounted for, clamped to zero rather than left negative.
func contentHeight(height int) int {
	h := height - chromeLines
	if h < 0 {
		return 0
	}
	return h
}

// splitWidths gives the list roughly two fifths of the screen and the detail
// pane the rest, less one column for the gap between them. Both results are
// clamped to zero rather than left negative, since a window can be narrower
// than the gap alone.
func splitWidths(width int) (left, right int) {
	if width < 0 {
		width = 0
	}
	const gap = 1
	left = width * 2 / 5
	right = width - left - gap
	if right < 0 {
		right = 0
	}
	return left, right
}

func wrapIndex(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

// footer names the keys the current mode responds to.
func (m Model) footer() string {
	switch m.mode {
	case modeChoosing:
		return "up/down choose  enter select  q quit"
	case modeBrowsing:
		return "up/down move  n/N group  tab fold  / search  q quit"
	default:
		return "q quit"
	}
}

// viewChoosing lists every discovered target, cursor on the one enter would load.
func (m Model) viewChoosing() string {
	var b strings.Builder
	for i, t := range m.targets {
		line := fmt.Sprintf("%s  %s", t.Title, m.reachability(t))
		style := m.wrapped()
		if i == m.choiceCursor {
			style = style.Background(m.theme.FocusBg)
		}
		b.WriteString(style.Render(line) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// viewBrowsing lays the list and the detail pane side by side, each already
// wrapped to the width applyLayout gave it, with any banner above them.
func (m Model) viewBrowsing() string {
	panes := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), " ", m.detail.View())
	if banner := m.browsingBanner(); banner != "" {
		return banner + "\n" + panes
	}
	return panes
}

// browsingBanner surfaces anything the loader collected instead of writing to
// the real terminal (Fetcher.Warnf lands here), so a cached spec served while
// the backend is down does not read as current.
func (m Model) browsingBanner() string {
	lines := append([]string(nil), m.warnings...)
	if m.stale && len(lines) == 0 {
		lines = append(lines, "browsing a cached spec; the service could not be reached to refresh it")
	}
	if len(lines) == 0 {
		return ""
	}
	return m.styled(m.theme.Warning).Render(strings.Join(lines, "\n"))
}
