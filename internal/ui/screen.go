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

// applyLayout splits the screen between the list and the detail pane, and
// gives both the height they have left once the chrome around them is
// accounted for. Called once per resize and once when a spec finishes
// loading, never per frame: detailModel caches its rendered markdown against
// width, and feeding it a fresh value every View() would defeat that cache.
func (m *Model) applyLayout() {
	left, right := splitWidths(m.width)
	m.list.SetWidth(left)
	m.detail.SetWidth(right)
	h := m.contentHeight()
	m.list.SetHeight(h)
	m.detail.SetHeight(h)
}

// contentHeight is how many rows the panes can show once the chrome around
// them is accounted for, clamped to zero rather than left negative.
func (m Model) contentHeight() int {
	h := m.height - m.chromeHeight()
	if h < 0 {
		return 0
	}
	return h
}

// chromeHeight measures the lines View actually draws outside the panes for
// the current mode, rather than assuming a fixed count: the title and the
// blank line under it, any banner, the blank line above the footer, and the
// footer itself, which can wrap onto more than one line at a narrow width.
// Deriving it this way means a later task adding more chrome (Tasks 12 and 13
// both will) costs nothing here.
func (m Model) chromeHeight() int {
	const titleAndBlank = 2
	const blankBeforeFooter = 1
	h := titleAndBlank + blankBeforeFooter
	if banner := m.browsingBanner(); banner != "" {
		h += lipgloss.Height(banner)
	}
	h += lipgloss.Height(m.styled(m.theme.Muted).Render(m.footer()))
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

// browsingBanner surfaces facts about the current load (loadNotes, such as
// the cache having been used instead of writing to the real terminal) and the
// loaded API's own advisories (warnings, already gated by the loader to the
// CLI's fresh-fetch rule so a cached spec does not nag on every frame).
func (m Model) browsingBanner() string {
	var lines []string
	lines = append(lines, m.loadNotes...)
	lines = append(lines, m.warnings...)
	if m.stale && len(lines) == 0 {
		lines = append(lines, "browsing a cached spec; the service could not be reached to refresh it")
	}
	if len(lines) == 0 {
		return ""
	}
	return m.styled(m.theme.Warning).Render(strings.Join(lines, "\n"))
}
