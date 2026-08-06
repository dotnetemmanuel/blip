package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/safety"
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
// step with whatever it now has selected, nil included. Once the Fill pane is
// open it owns the keyboard, so that arrow keys move between fields rather than
// moving the selection out from under the form.
func (m *Model) updateBrowsing(msg tea.KeyMsg) tea.Cmd {
	switch m.pane {
	case stateFill:
		return m.updateFilling(msg)
	case stateConfirm:
		return m.updateConfirming(msg)
	case stateSending:
		// Every key is ignored while a request is in flight except the ones the
		// root already handled, so a second s cannot send it twice.
		return nil
	case stateResult:
		return m.updateResult(msg)
	}

	// Enter picks what the cursor rests on, search box open or not: searching is
	// the fast way to reach an operation, so it has to be a way through rather
	// than a dead end. A header row has no operation behind it to pick.
	if msg.String() == "enter" {
		if op := m.list.Selected(); op != nil {
			m.list.commitSearch()
			m.pane = stateFill
			m.sendErr = nil
			m.form.SetOperation(op)
			m.applyLayout()
			return nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.detail.SetOperation(m.list.Selected())
	return cmd
}

// updateFilling gives the key to the form, except for the esc that closes it.
// esc while a field is taking text belongs to the field, which uses it to stop
// editing rather than to throw the whole form away.
func (m *Model) updateFilling(msg tea.KeyMsg) tea.Cmd {
	if !m.form.Editing() {
		switch msg.String() {
		case "esc":
			// The refusal is not cleared here. rightPane only draws it in the
			// fill pane, and the only way back in rebuilds the form and clears
			// it there, so a second clear would be a line no test could hold.
			m.pane = stateRead
			return nil
		case "s":
			return m.requestSend()
		}
	}
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	return cmd
}

// requestSend is the one door to the network. It refuses what readonly forbids,
// asks about anything else that mutates, and only then builds the request.
func (m *Model) requestSend() tea.Cmd {
	op := m.form.op
	if op == nil {
		return nil
	}
	m.sendErr = nil

	if m.send == nil || m.env == nil {
		m.sendErr = output.Configf("this target has no base URL to send to; browsing and filling in still work")
		return nil
	}
	// readonly is checked before anything is resolved or drawn, because it is
	// refused rather than offered, and the same gate the CLI uses answers it.
	gate := safety.Gate{Readonly: m.env.Readonly}
	if err := gate.Permitted(op.Method, m.env.Name); err != nil {
		m.sendErr = err
		return nil
	}
	// The request is built before the question is asked, so the confirmation can
	// name the URL that will actually be called rather than the path template.
	// Which order is about to be deleted is the whole point of asking.
	req, err := m.form.Resolve(m.ctx, m.env)
	if err != nil {
		m.sendErr = err
		return nil
	}
	m.pending = req

	if safety.IsMutation(op.Method) {
		m.pane = stateConfirm
		return nil
	}
	return m.performSend()
}

// performSend hands off the request that requestSend already built. Nothing can
// have edited the form in between: the confirmation owns the keyboard while it
// is up.
func (m *Model) performSend() tea.Cmd {
	req := m.pending
	m.pending = nil
	if req == nil {
		return nil
	}
	m.pane = stateSending
	return sendCmd(m.ctx, m.send, m.env, req)
}

// updateConfirming answers the mutation prompt. Anything that is not a yes is a
// no, so a stray key never sends a DELETE.
func (m *Model) updateConfirming(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		return m.performSend()
	default:
		m.pending = nil
		m.pane = stateFill
		return nil
	}
}

func (m *Model) updateResult(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.pane = stateFill
	case "up", "k":
		m.result.MoveCursor(-1)
	case "down", "j":
		m.result.MoveCursor(1)
	case "h":
		m.result.ToggleHeaders()
	case "tab":
		m.result.ToggleFold()
	}
	return nil
}

// applySent records the outcome of a send. A transport failure stays on screen
// as a failure rather than being drawn as an empty result.
func (m Model) applySent(msg sentMsg) Model {
	if m.pane != stateSending {
		// A reply that arrives after the screen moved on belongs to a send
		// nobody is waiting for any more.
		return m
	}
	if msg.err != nil {
		m.sendErr = msg.err
		m.pane = stateFill
		return m
	}
	m.sendErr = nil
	m.result.SetResponse(msg.method, msg.resp, msg.elapsed)
	m.pane = stateResult
	m.applyLayout()
	return m
}

// textEntryActive reports whether the focused pane is reading free text right
// now, so that a plain "q" types a letter there instead of quitting. Only
// ctrl+c is guaranteed to quit unconditionally.
func (m Model) textEntryActive() bool {
	if m.mode != modeBrowsing {
		return false
	}
	return m.list.searching || (m.pane == stateFill && m.form.Editing())
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
	m.form.SetWidth(right)
	m.result.SetWidth(right)
	h := m.contentHeight()
	m.list.SetHeight(h)
	m.detail.SetHeight(h)
	m.form.SetHeight(h)
	m.result.SetHeight(h)
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

// keys joins the hints a footer offers. Two spaces alone left "tab fold" and
// "/ search" reading as one instruction at a glance.
func keys(hints ...string) string {
	return strings.Join(hints, " · ")
}

// footer names the keys the current mode responds to.
func (m Model) footer() string {
	switch m.mode {
	case modeChoosing:
		return keys("up/down choose", "enter select", "q quit")
	case modeBrowsing:
		switch m.pane {
		case stateConfirm:
			return keys("y send it", "any other key cancels")
		case stateSending:
			return "sending"
		case stateResult:
			return keys("up/down move", "tab fold", "h headers", "esc back", "q quit")
		case stateFill:
			switch {
			case m.form.Editing():
				return keys("type to fill in", "esc done")
			case len(m.form.fields) == 0:
				return keys("s send", "esc back", "q quit")
			default:
				return keys("up/down field", "enter edit", "left/right choose", "s send", "esc back", "q quit")
			}
		}
		return keys("up/down move", "n/N group", "tab fold", "/ search", "enter fill in", "q quit")
	default:
		return keys("q quit")
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
	right := m.rightPane()
	panes := lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), " ", right)
	if banner := m.browsingBanner(); banner != "" {
		return banner + "\n" + panes
	}
	return panes
}

// rightPane is whichever of the four states the right-hand side is in. A send
// that did not happen replaces the pane entirely, so the reason is where the eye
// already is rather than in a corner.
func (m Model) rightPane() string {
	switch m.pane {
	case stateFill:
		return m.fillPane()
	case stateConfirm:
		return m.form.clamp(m.confirmView())
	case stateSending:
		return m.form.clamp(m.paneStyle(m.theme.Muted).Render("sending"))
	case stateResult:
		return m.result.View()
	default:
		return m.detail.View()
	}
}

// fillPane draws the form, with any refusal above it rather than instead of it.
// A value that needs fixing is in the form: replacing it would leave the reader
// either typing blind or starting over, since reopening rebuilds from the spec.
func (m Model) fillPane() string {
	if m.sendErr == nil {
		return m.form.View()
	}
	lines := strings.Split(m.paneStyle(m.theme.Error).Render(m.sendErr.Error()), "\n")

	form := m.form
	if room := form.height; room > 0 {
		// The message is bounded before the form is, or a refusal that wraps to
		// more lines than the pane is tall takes the whole layout with it.
		if room == 1 {
			return lines[0]
		}
		if len(lines) > room-1 {
			lines = lines[:room-1]
			// Cut from the tail, not wrapped and not cut from the head: a marker
			// that wraps costs the row it was counted as occupying, and
			// clipToWidth drops the leading words, which are the ones carrying
			// the instruction.
			lines[len(lines)-1] = lipgloss.NewStyle().Foreground(m.theme.Muted).
				Render(xansi.Truncate("… widen the window", m.form.width, "…"))
		}
		form.SetHeight(room - len(lines))
	}
	return strings.Join(lines, "\n") + "\n" + form.View()
}

// paneStyle wraps text to the right-hand pane rather than to the whole screen.
// A line wider than the pane runs past the edge and pushes the list out of step
// with it, which is what the whole layout is arranged to prevent.
func (m Model) paneStyle(c lipgloss.Color) lipgloss.Style {
	return styled(m.theme, m.form.width, c)
}

// confirmView spells out exactly what is about to change, because in a list
// navigated with arrow keys a delete is never more than a keystroke away.
func (m Model) confirmView() string {
	op := m.form.op
	if op == nil {
		return ""
	}
	target := op.Method + " " + op.Path
	if m.pending != nil {
		target = m.pending.Method + " " + m.pending.URL.String()
	}

	var b strings.Builder
	b.WriteString(m.paneStyle(methodColor(m.theme, op.Method)).Render("about to send"))
	b.WriteString("\n")
	b.WriteString(m.paneStyle(m.theme.Text).Render(target))
	if m.env != nil {
		b.WriteString("\n")
		b.WriteString(m.paneStyle(m.theme.Muted).Render("environment " + m.env.Name))
	}
	b.WriteString("\n\n")
	b.WriteString(m.paneStyle(m.theme.Warning).Render("y to send it, any other key to cancel"))
	return b.String()
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
