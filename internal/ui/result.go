package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/safety"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

const (
	stateConfirm paneState = "confirm"
	stateSending paneState = "sending"
	stateResult  paneState = "result"
)

// lineKind is what a body line holds, which decides both its colour and whether
// it names a value the cursor can pick up.
type lineKind int

const (
	kindOpen lineKind = iota
	kindClose
	kindString
	kindNumber
	kindLiteral
	kindRaw
)

// bodyLine is one rendered line of a response body, carrying the dotted path that
// reaches it so the cursor can report where it is without parsing the screen.
type bodyLine struct {
	depth int
	key   string
	text  string
	kind  lineKind
	comma bool

	path  string
	value string

	// end is the index of the line closing this container, so folding can skip
	// the range in one step rather than counting braces at render time.
	end int
}

// resultModel is the Send state: what came back, and a cursor over it.
type resultModel struct {
	theme  theme.Theme
	width  int
	height int

	method  string
	resp    *request.Response
	elapsed time.Duration

	lines       []bodyLine
	cursor      int
	folded      map[int]bool
	showHeaders bool
}

func newResultModel(t theme.Theme) resultModel {
	return resultModel{theme: t, folded: map[int]bool{}}
}

func (m *resultModel) SetWidth(width int)   { m.width = width }
func (m *resultModel) SetHeight(height int) { m.height = height }

// SetResponse loads what came back. The method comes along because a successful
// DELETE and a failed GET are different kinds of news and the theme colours them
// with different tokens.
func (m *resultModel) SetResponse(method string, resp *request.Response, elapsed time.Duration) {
	m.method, m.resp, m.elapsed = method, resp, elapsed
	m.cursor = 0
	m.folded = map[int]bool{}
	m.showHeaders = false
	m.lines = nil
	if resp != nil {
		m.lines = bodyLines(resp.Body)
	}
}

func (m *resultModel) ToggleHeaders() { m.showHeaders = !m.showHeaders }

// MoveCursor steps over the body lines that are currently visible, so a fold
// cannot strand the cursor inside something the screen is not showing.
func (m *resultModel) MoveCursor(delta int) {
	visible := m.visibleLines()
	if len(visible) == 0 {
		return
	}
	at := 0
	for i, idx := range visible {
		if idx == m.cursor {
			at = i
			break
		}
	}
	m.cursor = visible[wrapIndex(at+delta, len(visible))]
}

// ToggleFold collapses or reopens the container the cursor rests on.
func (m *resultModel) ToggleFold() {
	if m.cursor < 0 || m.cursor >= len(m.lines) {
		return
	}
	if m.lines[m.cursor].kind != kindOpen {
		return
	}
	m.folded[m.cursor] = !m.folded[m.cursor]
}

// ValueUnderCursor is the dotted path and value the cursor rests on. Both are
// empty on a line that only opens or closes a container, since there is no single
// value there to take.
func (m resultModel) ValueUnderCursor() (path, value string) {
	if m.cursor < 0 || m.cursor >= len(m.lines) {
		return "", ""
	}
	line := m.lines[m.cursor]
	return line.path, line.value
}

// visibleLines is the indices of the lines a fold has not hidden.
func (m resultModel) visibleLines() []int {
	var out []int
	for i := 0; i < len(m.lines); i++ {
		out = append(out, i)
		if m.lines[i].kind == kindOpen && m.folded[i] && m.lines[i].end > i {
			i = m.lines[i].end
		}
	}
	return out
}

func (m resultModel) View() string {
	if m.resp == nil {
		return styled(m.theme, m.width, m.theme.Muted).Render(clipToWidth("", "nothing sent yet", m.width))
	}

	rendered := []string{m.summary()}

	// An expanded header block can be taller than the pane on its own, so it is
	// bounded and says what it dropped. One row is always left for the body.
	headerBudget, budget := -1, -1
	if m.height > 0 {
		headerBudget = max(m.height-len(rendered)-1, 0)
	}
	headers := m.headerLines(headerBudget)
	if m.height > 0 {
		budget = max(m.height-len(rendered)-len(headers), 0)
	}

	rendered = append(rendered, headers...)
	rendered = append(rendered, m.bodyBlock(budget)...)
	return strings.Join(rendered, "\n")
}

// summary is the one line that answers "what happened": the status in the role
// that names its danger, then how long it took, how big it was, and what it says
// it is.
func (m resultModel) summary() string {
	status := strconv.Itoa(m.resp.Status)
	if text := http.StatusText(m.resp.Status); text != "" {
		status += " " + text
	}
	badge := lipgloss.NewStyle().Foreground(m.statusColor()).Bold(true).Render(status)

	parts := []string{formatElapsed(m.elapsed), formatSize(len(m.resp.Body))}
	if ct := m.resp.Header.Get("Content-Type"); ct != "" {
		parts = append(parts, strings.TrimSpace(strings.Split(ct, ";")[0]))
	}
	rest := lipgloss.NewStyle().Foreground(m.theme.Muted).Render(" · " + strings.Join(parts, " · "))
	return clipToWidth(badge, rest, m.width)
}

// statusColor keeps three different pieces of news apart: a failing status is an
// error, a mutation that worked is still something that changed the world, and a
// read that worked is plain good news.
func (m resultModel) statusColor() lipgloss.Color {
	switch {
	case m.resp.Status >= 400:
		return m.theme.Error
	case safety.IsMutation(m.method):
		return m.theme.Danger
	default:
		return m.theme.Success
	}
}

// headerLines draws the header block within budget, where a budget below zero
// means no limit. Dropping headers silently would have the reader conclude the
// one they were looking for was never sent.
func (m resultModel) headerLines(budget int) []string {
	muted := func(s string) string {
		return styled(m.theme, m.width, m.theme.Muted).Render(clipToWidth("", s, m.width))
	}
	count := len(m.resp.Header)
	if !m.showHeaders {
		return []string{muted(fmt.Sprintf("%d header(s), h to show", count))}
	}

	names := make([]string, 0, count)
	for name := range m.resp.Header {
		names = append(names, name)
	}
	sort.Strings(names)

	out := []string{muted(fmt.Sprintf("%d header(s), h to hide", count))}
	for i, name := range names {
		// The last row is kept for the marker, but only when something is
		// actually left over: claiming the pane is too short for headers that
		// would have fitted is its own kind of lie.
		if budget >= 0 && len(out)+(len(names)-i) > budget && len(out)+1 >= budget {
			out = append(out, muted(fmt.Sprintf("… %d more header(s); the pane is too short", len(names)-i)))
			break
		}
		key := lipgloss.NewStyle().Foreground(m.theme.Info).Render("  " + name + ": ")
		out = append(out, clipToWidth(key, strings.Join(m.resp.Header[name], ", "), m.width))
	}
	if budget >= 0 && len(out) > budget {
		out = out[:max(budget, 1)]
	}
	return out
}

// bodyBlock renders the lines the window will actually show and no others. A
// budget below zero means no limit. Rendering the whole body and then cutting it
// down costs the same on every keystroke whether twenty lines are on screen or
// twenty thousand.
func (m resultModel) bodyBlock(budget int) []string {
	if len(m.resp.Body) == 0 {
		if budget == 0 {
			return nil
		}
		return []string{styled(m.theme, m.width, m.theme.Muted).Render(clipToWidth("", "no body", m.width))}
	}

	visible := m.visibleLines()
	focus := 0
	for i, idx := range visible {
		if idx == m.cursor {
			focus = i
			break
		}
	}

	start, end := windowRange(len(visible), focus, budget)
	lines := make([]string, 0, end-start)
	for _, idx := range visible[start:end] {
		lines = append(lines, m.renderLine(idx))
	}
	return lines
}

func (m resultModel) renderLine(idx int) string {
	line := m.lines[idx]
	prefix := strings.Repeat("  ", line.depth)
	if line.key != "" {
		prefix += lipgloss.NewStyle().Foreground(m.theme.Info).Render(strconv.Quote(line.key) + ": ")
	}

	text := line.text
	comma := line.comma
	if line.kind == kindOpen && m.folded[idx] {
		text += " … " + closingOf(text)
		// The comma belongs to the closing line, which the fold just hid, so a
		// folded container would otherwise read as the last member of its parent.
		if line.end > idx && line.end < len(m.lines) {
			comma = m.lines[line.end].comma
		}
	}
	if comma {
		text += ","
	}

	body := lipgloss.NewStyle().Foreground(m.lineColor(line)).Render(text)
	style := wrapped(m.theme, m.width)
	if idx == m.cursor {
		style = style.Background(m.theme.FocusBg)
	}
	return style.Render(clipToWidth(prefix, body, m.width))
}

func closingOf(open string) string {
	if strings.HasPrefix(open, "[") {
		return "]"
	}
	return "}"
}

func (m resultModel) lineColor(line bodyLine) lipgloss.Color {
	switch line.kind {
	case kindString, kindRaw:
		return m.theme.Text
	case kindNumber:
		return m.theme.Warning
	case kindLiteral:
		return m.theme.Muted
	default:
		return m.theme.Muted
	}
}

func formatElapsed(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

func formatSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f kB", float64(n)/1024)
}

// A response is whatever the far end chose to send, so the walk is bounded twice
// over. Each level's dotted path carries its parent's, which makes the memory
// quadratic in depth: without a limit a megabyte of "[[[[..." would allocate
// terabytes of path strings and take the machine down with it. The line cap
// bounds a body that is merely enormous rather than deep.
const (
	maxBodyDepth = 64
	maxBodyLines = 20000
)

// errBodyTooBig stops the walk without pretending the result is the whole
// document, so the caller shows what arrived rather than a plausible prefix.
var errBodyTooBig = errors.New("response body is too deep or too long to lay out")

// bodyLines turns a response body into lines that each know their own path. A
// body that is not JSON is shown as it arrived rather than swallowed, because a
// plain text error page is often the most useful thing on the screen.
func bodyLines(data []byte) []bodyLine {
	if len(data) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var lines []bodyLine
	if err := walkJSON(dec, "", "", 0, &lines); err != nil {
		return rawLines(data)
	}
	// Anything but a clean end of input means this was not one JSON document, so
	// showing the prefix that happened to parse would be a lie about what came
	// back. Trailing junk fails to tokenize, which is not the same as being over.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return rawLines(data)
	}
	return lines
}

// rawLines is the fallback for a body blip will not lay out, so it carries the
// same cap: the fallback used to be the unbounded path, which meant exceeding the
// limit routed you into the very thing the limit exists to prevent.
func rawLines(data []byte) []bodyLine {
	// Walked rather than split, so a body far past the cap never materialises
	// every line just to throw most of them away.
	text := strings.TrimRight(string(data), "\n")
	var out []bodyLine
	for {
		if len(out) >= maxBodyLines {
			out = append(out, bodyLine{
				text: fmt.Sprintf("… %d more line(s); too long to lay out", strings.Count(text, "\n")+1),
				kind: kindLiteral,
			})
			return out
		}
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			return append(out, bodyLine{text: text, kind: kindRaw})
		}
		out = append(out, bodyLine{text: text[:i], kind: kindRaw})
		text = text[i+1:]
	}
}

func walkJSON(dec *json.Decoder, path, key string, depth int, lines *[]bodyLine) error {
	if depth > maxBodyDepth || len(*lines) > maxBodyLines {
		return errBodyTooBig
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}

	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		*lines = append(*lines, scalarLine(tok, path, key, depth))
		return nil
	}

	start := len(*lines)
	*lines = append(*lines, bodyLine{depth: depth, key: key, text: string(delim), kind: kindOpen})

	for i := 0; dec.More(); i++ {
		childPath, childKey := path, ""
		if delim == '{' {
			nameTok, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := nameTok.(string)
			if !ok {
				return fmt.Errorf("object key is %T, not a string", nameTok)
			}
			// A key containing a dot makes this ambiguous: {"a.b":1} and
			// {"a":{"b":1}} both read as a.b. Nothing downstream can tell them
			// apart, which matters when Task 14 labels a captured token.
			childKey = name
			childPath = name
			if path != "" {
				childPath = path + "." + name
			}
		} else {
			childPath = fmt.Sprintf("%s[%d]", path, i)
		}

		if err := walkJSON(dec, childPath, childKey, depth+1, lines); err != nil {
			return err
		}
		if dec.More() {
			(*lines)[len(*lines)-1].comma = true
		}
	}

	if _, err := dec.Token(); err != nil {
		return err
	}
	*lines = append(*lines, bodyLine{depth: depth, text: closingOf(string(delim)), kind: kindClose})
	(*lines)[start].end = len(*lines) - 1
	return nil
}

func scalarLine(tok any, path, key string, depth int) bodyLine {
	line := bodyLine{depth: depth, key: key, path: path}
	switch v := tok.(type) {
	case string:
		line.text, line.value, line.kind = strconv.Quote(v), v, kindString
	case json.Number:
		line.text, line.value, line.kind = v.String(), v.String(), kindNumber
	case bool:
		line.text = strconv.FormatBool(v)
		line.value, line.kind = line.text, kindLiteral
	default:
		line.text, line.value, line.kind = "null", "null", kindLiteral
	}
	return line
}
