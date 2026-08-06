package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

// sendKey is what the fill pane binds to "send this now".
var sendKey = rune1('s')

// recorder is a server that remembers whether anything reached it.
type recorder struct {
	*httptest.Server
	got    []string
	status int
	body   string
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{status: http.StatusOK, body: `{"ok":true}`}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.got = append(r.got, req.Method+" "+req.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(r.status)
		_, _ = w.Write([]byte(r.body))
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) sent() string { return strings.Join(r.got, ", ") }

// sendingModel is a browsing model wired to a live server, with one operation of
// the given method so a test can choose whether it is a mutation.
func sendingModel(t *testing.T, srv *recorder, method string, readonly bool) Model {
	t.Helper()
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing %q: %v", srv.URL, err)
	}
	env := &config.Environment{Name: "dev", BaseURL: base, Readonly: readonly}

	spec := `{"openapi":"3.0.1","info":{"title":"S","version":"1"},"paths":{
		"/api/things/{id}":{"` + strings.ToLower(method) + `":{"tags":["Things"],"operationId":"op",
		 "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
		 "responses":{"200":{"description":"OK"}}}}}}`

	m := New(context.Background(), Options{
		Send: func(ctx context.Context, _ *config.Environment, req *http.Request) (*request.Response, error) {
			return request.Do(srv.Client(), req.WithContext(ctx))
		},
	})
	m.list.SetAPI(mustParse(t, spec))
	m.mode = modeBrowsing
	m.env = env
	m.detail.SetOperation(m.list.Selected())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model)
}

// openForm puts the cursor in the fill pane with the path parameter filled, which
// is the state a send is reachable from.
func openForm(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(enterKey)
	m = next.(Model)
	if m.pane != stateFill {
		t.Fatalf("test setup: pane = %q, want the fill pane", m.pane)
	}
	fill(t, &m.form, "path:id", "7")
	return m
}

// drain runs a command and feeds its message back, which is what the bubbletea
// runtime does and what a test has to do by hand.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil && i < 5; i++ {
		msg := cmd()
		if msg == nil {
			return m
		}
		next, c := m.Update(msg)
		m, cmd = next.(Model), c
	}
	return m
}

func TestGetSendsWithoutConfirmation(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "GET", false))

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	if srv.sent() != "GET /api/things/7" {
		t.Errorf("server saw %q, want the request sent without a prompt", srv.sent())
	}
	if m.pane != stateResult {
		t.Errorf("pane = %q, want the result", m.pane)
	}
}

func TestMutationAsksBeforeSendingAndSendsOnlyWhenAccepted(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "DELETE", false))

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	if m.pane != stateConfirm {
		t.Fatalf("pane = %q, want a confirmation", m.pane)
	}
	if srv.sent() != "" {
		t.Fatalf("server saw %q before the confirmation was answered", srv.sent())
	}
	if view := m.View(); !strings.Contains(view, "DELETE") {
		t.Errorf("the confirmation does not name the method:\n%s", view)
	}

	next, cmd = m.Update(rune1('y'))
	m = drain(t, next.(Model), cmd)

	if srv.sent() != "DELETE /api/things/7" {
		t.Errorf("server saw %q, want the delete sent once accepted", srv.sent())
	}
}

func TestDecliningAMutationSendsNothing(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "DELETE", false))

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)
	next, cmd = m.Update(rune1('n'))
	m = drain(t, next.(Model), cmd)

	if srv.sent() != "" {
		t.Errorf("server saw %q, want nothing sent after declining", srv.sent())
	}
	if m.pane != stateFill {
		t.Errorf("pane = %q, want to be back in the form", m.pane)
	}
}

// readonly is refused, never offered: a prompt would imply it could be answered.
func TestReadonlyRefusesAMutationWithoutOfferingAConfirmation(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "DELETE", true))

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	if m.pane == stateConfirm {
		t.Fatal("readonly offered a confirmation, want it refused outright")
	}
	if srv.sent() != "" {
		t.Errorf("server saw %q, want nothing sent in a readonly environment", srv.sent())
	}
	view := m.View()
	if !strings.Contains(view, "readonly") {
		t.Errorf("the refusal does not say why:\n%s", view)
	}
	// Answering the prompt that is not there must not send it either.
	next, cmd = m.Update(rune1('y'))
	m = drain(t, next.(Model), cmd)
	if srv.sent() != "" {
		t.Errorf("server saw %q after a y in a readonly environment", srv.sent())
	}
}

func TestReadonlyStillSendsAGet(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "GET", true))

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	if srv.sent() != "GET /api/things/7" {
		t.Errorf("server saw %q, want a read to go through in a readonly environment", srv.sent())
	}
}

func TestSendRefusesBeforeTheNetworkWhenAValueIsBad(t *testing.T) {
	srv := newRecorder(t)
	m := sendingModel(t, srv, "GET", false)
	next, _ := m.Update(enterKey)
	m = next.(Model)
	fill(t, &m.form, "path:id", "../admin")

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	if srv.sent() != "" {
		t.Errorf("server saw %q, want a climbing value refused before the network", srv.sent())
	}
	if !strings.Contains(m.View(), "..") {
		t.Errorf("the refusal does not name the segment:\n%s", m.View())
	}
}

// A failing status and a mutating method are two different kinds of bad news, and
// the theme gives them two different tokens. A render that used one for both
// would look right until a theme moved them apart.
func TestFailureAndDangerUseDistinctRoles(t *testing.T) {
	forceTrueColor(t)
	th := theme.Theme{Error: lipgloss.Color("#ff0000"), Danger: lipgloss.Color("#00ff00"), Success: lipgloss.Color("#0000ff")}

	failed := newResultModel(th)
	failed.SetWidth(60)
	failed.SetResponse("GET", &request.Response{Status: 500, Header: http.Header{}}, time.Millisecond)

	deleted := newResultModel(th)
	deleted.SetWidth(60)
	deleted.SetResponse("DELETE", &request.Response{Status: 200, Header: http.Header{}}, time.Millisecond)

	if got := failed.View(); !strings.Contains(got, "255;0;0") {
		t.Errorf("a 500 does not use the error role:\n%q", got)
	}
	if got := deleted.View(); !strings.Contains(got, "0;255;0") {
		t.Errorf("a successful DELETE does not use the danger role:\n%q", got)
	}
	if got := deleted.View(); strings.Contains(got, "255;0;0") {
		t.Errorf("a successful DELETE reads as an error:\n%q", got)
	}
}

func TestResultShowsStatusTimingAndSize(t *testing.T) {
	m := newResultModel(theme.Theme{})
	m.SetWidth(80)
	m.SetHeight(20)
	m.SetResponse("GET", &request.Response{
		Status: 201,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"id":1}`),
	}, 42*time.Millisecond)

	view := m.View()
	for _, want := range []string{"201", "42ms", "8 B"} {
		if !strings.Contains(view, want) {
			t.Errorf("the summary is missing %q:\n%s", want, view)
		}
	}
}

// Headers are folded away by default: a response has many and the body is the
// thing being read.
func TestHeadersAreFoldedUntilAskedFor(t *testing.T) {
	m := newResultModel(theme.Theme{})
	m.SetWidth(80)
	m.SetHeight(30)
	m.SetResponse("GET", &request.Response{
		Status: 200,
		Header: http.Header{"X-Trace": {"abc123"}},
		Body:   []byte(`{}`),
	}, time.Millisecond)

	if strings.Contains(m.View(), "abc123") {
		t.Fatal("headers are shown before being asked for")
	}
	m.ToggleHeaders()
	if !strings.Contains(m.View(), "abc123") {
		t.Errorf("headers stayed hidden after being asked for:\n%s", m.View())
	}
}

func TestValueUnderCursorReportsADottedPath(t *testing.T) {
	body := `{"order":{"id":"7f00","lines":[{"sku":"A1","qty":2}],"paid":true,"note":null}}`
	m := newResultModel(theme.Theme{})
	m.SetWidth(80)
	m.SetHeight(40)
	m.SetResponse("GET", &request.Response{Status: 200, Header: http.Header{}, Body: []byte(body)}, time.Millisecond)

	want := map[string]string{
		"order.id":           "7f00",
		"order.lines[0].sku": "A1",
		"order.lines[0].qty": "2",
		"order.paid":         "true",
		"order.note":         "null",
	}
	found := map[string]string{}
	for i := 0; i < 60; i++ {
		if path, value := m.ValueUnderCursor(); path != "" {
			found[path] = value
		}
		m.MoveCursor(1)
	}

	for path, value := range want {
		got, ok := found[path]
		if !ok {
			t.Errorf("no line reports the path %q; found %v", path, found)
			continue
		}
		if got != value {
			t.Errorf("%s = %q, want %q", path, got, value)
		}
	}
}

func TestValueUnderCursorIsEmptyOnAStructuralLine(t *testing.T) {
	m := newResultModel(theme.Theme{})
	m.SetWidth(80)
	m.SetHeight(40)
	m.SetResponse("GET", &request.Response{Status: 200, Header: http.Header{}, Body: []byte(`{"a":{"b":1}}`)}, time.Millisecond)

	// The first body line is the opening brace, which is not a value.
	for i := 0; i < 40; i++ {
		path, value := m.ValueUnderCursor()
		if path == "" && value != "" {
			t.Fatalf("a line with no path still reports the value %q", value)
		}
		m.MoveCursor(1)
	}
}

// A body that is not JSON must still be readable rather than swallowed.
func TestNonJSONBodyIsShownAsItIs(t *testing.T) {
	m := newResultModel(theme.Theme{})
	m.SetWidth(80)
	m.SetHeight(20)
	m.SetResponse("GET", &request.Response{
		Status: 200,
		Header: http.Header{"Content-Type": {"text/plain"}},
		Body:   []byte("not json at all"),
	}, time.Millisecond)

	if !strings.Contains(m.View(), "not json at all") {
		t.Errorf("a plain text body is not shown:\n%s", m.View())
	}
}

func TestEmptyBodySaysSoRatherThanDrawingNothing(t *testing.T) {
	m := newResultModel(theme.Theme{})
	m.SetWidth(80)
	m.SetHeight(20)
	m.SetResponse("DELETE", &request.Response{Status: 204, Header: http.Header{}}, time.Millisecond)

	view := m.View()
	if !strings.Contains(view, "204") {
		t.Errorf("the status is missing:\n%s", view)
	}
	if !strings.Contains(strings.ToLower(view), "no body") {
		t.Errorf("an empty body is not accounted for:\n%s", view)
	}
}

func TestResultFitsTheHeightItWasGiven(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 200; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"n":`)
		b.WriteString(strings.Repeat("9", 3))
		b.WriteString(`}`)
	}
	b.WriteString("]}")

	m := newResultModel(theme.Theme{})
	m.SetWidth(60)
	m.SetHeight(10)
	m.SetResponse("GET", &request.Response{Status: 200, Header: http.Header{}, Body: []byte(b.String())}, time.Millisecond)

	if got := strings.Count(m.View(), "\n") + 1; got > 10 {
		t.Errorf("the result drew %d lines, want at most 10", got)
	}
}

func TestResultClipsRatherThanWraps(t *testing.T) {
	m := newResultModel(theme.Theme{})
	m.SetWidth(24)
	m.SetHeight(20)
	m.SetResponse("GET", &request.Response{
		Status: 200,
		Header: http.Header{},
		Body:   []byte(`{"averyveryverylongkeyname":"and a very long value to go with it"}`),
	}, time.Millisecond)

	lines := strings.Split(m.View(), "\n")
	for _, line := range lines {
		if got := lineWidth(line); got > 24 {
			t.Errorf("line %q is %d wide, want at most 24", line, got)
		}
	}
	// Three body lines plus the summary; wrapping would make more.
	if len(lines) > 6 {
		t.Errorf("the result drew %d lines for a four line response, so a row wrapped:\n%s", len(lines), m.View())
	}
}

func TestTransportFailureIsShownRatherThanSwallowed(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "GET", false))
	srv.Close()

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	if m.pane == stateResult {
		t.Fatal("a failed send rendered as a result")
	}
	if strings.TrimSpace(m.View()) == "" {
		t.Fatal("a failed send left the screen blank")
	}
}

// The confirmation exists to say which thing is about to change, so it has to
// name the resolved URL. A path template would leave you agreeing to delete
// {id} with no way to tell which order that is.
func TestConfirmationNamesTheResolvedURLNotTheTemplate(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "DELETE", false))

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	// The left pane legitimately shows the template, so this asks the
	// confirmation itself rather than the whole screen.
	pane := m.rightPane()
	if !strings.Contains(pane, "/api/things/7") {
		t.Errorf("the confirmation does not name the resolved URL:\n%s", pane)
	}
	if strings.Contains(pane, "{id}") {
		t.Errorf("the confirmation still shows the path template:\n%s", pane)
	}
	if !strings.Contains(pane, srv.URL) {
		t.Errorf("the confirmation does not name the host it would reach:\n%s", pane)
	}
}

// A value the form would refuse must be refused before the question, not after
// it: agreeing to a send that then fails teaches the wrong thing about the gate.
func TestAMutationWithABadValueIsRefusedBeforeTheQuestion(t *testing.T) {
	srv := newRecorder(t)
	m := sendingModel(t, srv, "DELETE", false)
	next, _ := m.Update(enterKey)
	m = next.(Model)
	fill(t, &m.form, "path:id", "../admin")

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	if m.pane == stateConfirm {
		t.Fatal("a climbing value reached the confirmation, want it refused first")
	}
	if srv.sent() != "" {
		t.Errorf("server saw %q", srv.sent())
	}
}

// Every state the right-hand side can be in has to stay inside the pane. A line
// wider than the pane runs past the screen edge and pushes the list out of step
// with whatever sits beside it.
func TestEveryRightPaneStateStaysInsideThePane(t *testing.T) {
	srv := newRecorder(t)
	srv.body = `{"message":"a rather long message that will not fit inside a narrow pane at all"}`

	const width = 90
	states := map[string]func() Model{
		"confirm": func() Model {
			m := openForm(t, sendingModel(t, srv, "DELETE", false))
			next, cmd := m.Update(sendKey)
			return drain(t, next.(Model), cmd)
		},
		"refusal": func() Model {
			m := openForm(t, sendingModel(t, srv, "DELETE", true))
			next, cmd := m.Update(sendKey)
			return drain(t, next.(Model), cmd)
		},
		"result": func() Model {
			m := openForm(t, sendingModel(t, srv, "GET", false))
			next, cmd := m.Update(sendKey)
			return drain(t, next.(Model), cmd)
		},
	}

	for name, build := range states {
		t.Run(name, func(t *testing.T) {
			m := build()
			next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = next.(Model)

			_, right := splitWidths(width)
			for _, line := range strings.Split(m.rightPane(), "\n") {
				if got := lineWidth(line); got > right {
					t.Errorf("line %q is %d wide, want at most the pane's %d", line, got, right)
				}
			}
			for _, line := range strings.Split(m.View(), "\n") {
				if got := lineWidth(line); got > width {
					t.Errorf("the screen line %q is %d wide, want at most %d", line, got, width)
				}
			}
		})
	}
}

// A folded container keeps the comma that followed it. Without one the body
// reads as though the container were the last member of its parent, which is a
// lie about the shape of the response.
func TestFoldingKeepsTheTrailingComma(t *testing.T) {
	m := newResultModel(theme.Theme{})
	m.SetWidth(80)
	m.SetHeight(40)
	m.SetResponse("GET", &request.Response{
		Status: 200,
		Header: http.Header{},
		Body:   []byte(`{"items":[1,2],"page":1}`),
	}, time.Millisecond)

	// Cursor onto the "items" line, then fold it.
	m.MoveCursor(1)
	if _, ok := m.ValueUnderCursor(); ok != "" {
		t.Fatalf("test setup: the cursor is on a value, want it on the items container")
	}
	m.ToggleFold()

	view := m.View()
	if !strings.Contains(view, "] ,") && !strings.Contains(view, "],") {
		t.Errorf("the folded container lost its comma:\n%s", view)
	}
	if !strings.Contains(view, "…") {
		t.Errorf("the container is not folded:\n%s", view)
	}
	if strings.Contains(view, `"page"`) == false {
		t.Errorf("folding hid a sibling:\n%s", view)
	}
}

// A response is whatever the far end chose to send. Each level's dotted path
// carries its parent's, so laying out a deeply nested body costs memory
// quadratic in its depth: an unbounded walk turns a megabyte of "[[[[..." into
// terabytes of path strings and takes the machine down with it. These inputs are
// deliberately small enough to be safe if the bound is ever removed.
func TestBodyLinesRefusesToLayOutAPathologicalBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "nested past the depth limit", body: strings.Repeat("[", 200) + strings.Repeat("]", 200)},
		{name: "more lines than the cap", body: "[" + strings.TrimSuffix(strings.Repeat("1,", 30000), ",") + "]"},
		{name: "raw text past the cap", body: strings.Repeat("line\n", 30000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Called straight, with inputs small enough to be harmless even if
			// the bound were deleted. A goroutine abandoned behind a timeout is
			// what took this machine down once: nothing stops it when the test
			// gives up, and it keeps allocating.
			if n := len(bodyLines([]byte(tt.body))); n > maxBodyLines+2 {
				t.Errorf("laid out %d lines, want the walk bounded near %d", n, maxBodyLines)
			}
		})
	}
}

func TestBodyLinesStillLaysOutAnOrdinaryNestedBody(t *testing.T) {
	body := `{"a":{"b":{"c":{"d":[{"e":1}]}}}}`
	lines := bodyLines([]byte(body))

	if len(lines) == 0 {
		t.Fatal("an ordinary nested body produced nothing")
	}
	var paths []string
	for _, l := range lines {
		if l.path != "" {
			paths = append(paths, l.path)
		}
	}
	if len(paths) != 1 || paths[0] != "a.b.c.d[0].e" {
		t.Errorf("paths = %v, want a.b.c.d[0].e", paths)
	}
}

// The fallback for a body blip will not lay out has to carry the same cap. It
// used to be the unbounded path, so exceeding the limit routed you into the very
// thing the limit exists to prevent.
func TestRawFallbackIsCappedToo(t *testing.T) {
	body := strings.Repeat("a log line\n", maxBodyLines+5000)

	lines := bodyLines([]byte(body))

	if len(lines) > maxBodyLines+1 {
		t.Errorf("the raw fallback produced %d lines against a cap of %d", len(lines), maxBodyLines)
	}
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1].text, "more line") {
		t.Errorf("nothing says the body was cut short: last line = %q", lines[len(lines)-1].text)
	}
}

// Only what the window will show gets rendered. Laying out the whole body on
// every keystroke costs the same whether twenty lines are on screen or twenty
// thousand, which is what makes a big reply unusable rather than merely slow.
func TestOnlyTheVisibleBodyLinesAreRendered(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 4000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"n":1}`)
	}
	b.WriteString("]}")

	m := newResultModel(theme.Theme{})
	m.SetWidth(60)
	m.SetHeight(12)
	m.SetResponse("GET", &request.Response{Status: 200, Header: http.Header{}, Body: []byte(b.String())}, time.Millisecond)

	if got := len(m.lines); got < 1000 {
		t.Fatalf("test setup: only %d body lines, want a body far taller than the pane", got)
	}
	if got := strings.Count(m.View(), "\n") + 1; got > 12 {
		t.Errorf("the result drew %d lines on a 12 line pane", got)
	}

	start := time.Now()
	for i := 0; i < 200; i++ {
		m.MoveCursor(1)
		_ = m.View()
	}
	// Rendering every line each frame put this at seconds rather than
	// milliseconds; the bound is loose enough not to be flaky on a busy machine.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("200 keystrokes took %v, so the whole body is being rendered each frame", elapsed)
	}
}

// Expanding the headers must not push the pane past its height and shove the
// footer off the screen.
func TestExpandedHeadersStayInsideTheHeight(t *testing.T) {
	header := http.Header{}
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M"} {
		header.Set("X-"+name, "value")
	}

	m := newResultModel(theme.Theme{})
	m.SetWidth(60)
	m.SetHeight(10)
	m.SetResponse("GET", &request.Response{Status: 200, Header: header, Body: []byte(`{"a":1,"b":2}`)}, time.Millisecond)
	m.ToggleHeaders()

	if got := strings.Count(m.View(), "\n") + 1; got > 10 {
		t.Errorf("the result drew %d lines on a 10 line pane with headers open:\n%s", got, m.View())
	}
}

// A reply with junk after the JSON must not be shown as though the JSON were all
// of it. A proxy appending a warning is the common way this happens.
func TestTrailingJunkAfterJSONShowsTheWholeBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "html fragment", body: `{"ok":true} <html>upstream error</html>`},
		{name: "warning line", body: "{\"ok\":true}\nWarning: connection reset by upstream"},
		{name: "second document", body: `{"ok":true}{"second":1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newResultModel(theme.Theme{})
			m.SetWidth(120)
			m.SetHeight(40)
			m.SetResponse("GET", &request.Response{Status: 200, Header: http.Header{}, Body: []byte(tt.body)}, time.Millisecond)

			view := m.View()
			tail := strings.TrimSpace(tt.body[strings.Index(tt.body, "}")+1:])
			if !strings.Contains(view, strings.Fields(tail)[0]) {
				t.Errorf("what followed the JSON is not on screen:\n%s", view)
			}
		})
	}
}

// A refusal must not outlive the form it came from, and must not replace it
// either. Both halves are asserted, because either mechanism alone hid the
// other: the round that fixed the first bug introduced the second.
func TestARefusalIsShownWithTheFormAndDoesNotOutliveIt(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "DELETE", true))

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	pane := m.rightPane()
	if !strings.Contains(pane, "readonly") {
		t.Fatalf("the refusal is not on screen:\n%s", pane)
	}
	// The form is still there under the message, with what was typed in it.
	if !strings.Contains(pane, "id") || !strings.Contains(pane, "7") {
		t.Errorf("the refusal replaced the form instead of sitting above it:\n%s", pane)
	}

	next, _ = m.Update(esc)
	m = next.(Model)
	if strings.Contains(m.rightPane(), "readonly") {
		t.Errorf("the refusal is still there after leaving the form:\n%s", m.rightPane())
	}
	if m.pane != stateRead {
		t.Errorf("pane = %q, want the detail pane back", m.pane)
	}

	// Reopening the form must not bring the old message back with it.
	next, _ = m.Update(enterKey)
	m = next.(Model)
	if strings.Contains(m.rightPane(), "readonly") {
		t.Errorf("the refusal followed the form when it was reopened:\n%s", m.rightPane())
	}
}

// A validation refusal is about a value in the form, so the form has to stay in
// front of you: replacing it means typing blind, and leaving to clear it loses
// everything typed, since reopening rebuilds from the spec.
func TestAValidationRefusalKeepsTheFilledInFormOnScreen(t *testing.T) {
	srv := newRecorder(t)
	m := sendingModel(t, srv, "GET", false)
	next, _ := m.Update(enterKey)
	m = next.(Model)
	fill(t, &m.form, "path:id", "../admin")

	next, cmd := m.Update(sendKey)
	m = drain(t, next.(Model), cmd)

	pane := m.rightPane()
	if !strings.Contains(pane, "..") {
		t.Fatalf("the refusal does not name the segment:\n%s", pane)
	}
	if !strings.Contains(pane, "../admin") {
		t.Errorf("the value that needs fixing is not on screen to fix:\n%s", pane)
	}
	if srv.sent() != "" {
		t.Errorf("server saw %q", srv.sent())
	}
}

// The header block says what it dropped. Silently showing six of thirteen has
// the reader conclude the one they were looking for was never sent.
func TestATrimmedHeaderBlockSaysWhatItDropped(t *testing.T) {
	header := http.Header{}
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M"} {
		header.Set("X-"+name, "value")
	}

	m := newResultModel(theme.Theme{})
	m.SetWidth(60)
	m.SetHeight(10)
	m.SetResponse("GET", &request.Response{Status: 200, Header: header, Body: []byte(`{"a":1}`)}, time.Millisecond)
	m.ToggleHeaders()

	view := m.View()
	if !strings.Contains(view, "more header(s)") {
		t.Errorf("headers were dropped with nothing saying so:\n%s", view)
	}
	// The count stays on screen once expanded, or there is no way to tell how
	// many there were.
	if !strings.Contains(view, "13 header(s)") {
		t.Errorf("the expanded block does not say how many headers there are:\n%s", view)
	}
}

// A reply that arrives after the screen moved on belongs to a send nobody is
// waiting for, and must not redraw the pane out from under whatever is there now.
func TestALateReplyDoesNotDisturbTheScreen(t *testing.T) {
	srv := newRecorder(t)
	m := openForm(t, sendingModel(t, srv, "GET", false))
	before := m.pane

	next, _ := m.Update(sentMsg{method: "GET", resp: &request.Response{Status: 200, Header: http.Header{}}})
	m = next.(Model)

	if m.pane != before {
		t.Errorf("pane = %q, want it left at %q", m.pane, before)
	}
}

// A refusal can be longer than the pane is tall once it wraps. It gets bounded
// before the form does, or the whole layout goes with it: the list beside it is
// padded out of step and the footer is pushed off the bottom.
func TestALongRefusalStaysInsideThePane(t *testing.T) {
	// Verbatim, newlines included: the real refusal wraps differently from a
	// flattened one and overflows at more sizes than a single long line does.
	long := "profile \"x\" has no hosts list, so blip will not send it to api.example.test.\n" +
		"       Add this to the [x] profile in ~/.config/blip/credentials.toml:\n" +
		"         hosts = [\"api.example.test\"]"

	for _, size := range []struct{ width, height int }{
		{40, 12}, {60, 12}, {70, 12}, {80, 10}, {80, 14}, {80, 24}, {100, 30},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			srv := newRecorder(t)
			m := openForm(t, sendingModel(t, srv, "GET", false))
			next, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = next.(Model)
			m.sendErr = errors.New(long)

			_, right := splitWidths(size.width)
			pane := m.rightPane()
			if got := lipgloss.Height(pane); got > m.form.height {
				t.Errorf("the pane drew %d rows into %d:\n%s", got, m.form.height, pane)
			}
			for _, line := range strings.Split(pane, "\n") {
				if got := lineWidth(line); got > right {
					t.Errorf("line %q is %d wide, want at most %d", line, got, right)
				}
			}
			if got := lipgloss.Height(m.View()); got > size.height {
				t.Errorf("the screen drew %d rows on a %d row terminal", got, size.height)
			}
		})
	}
}

// Claiming the pane is too short for headers that would have fitted is its own
// kind of lie.
func TestHeadersThatFitAreNotReportedAsDropped(t *testing.T) {
	for n := 1; n <= 6; n++ {
		header := http.Header{}
		for i := 0; i < n; i++ {
			header.Set(fmt.Sprintf("X-H%d", i), "value")
		}

		m := newResultModel(theme.Theme{})
		m.SetWidth(60)
		// Room for the summary, the label, every header, and one body line.
		m.SetHeight(n + 3)
		m.SetResponse("GET", &request.Response{Status: 200, Header: header, Body: []byte(`{}`)}, time.Millisecond)
		m.ToggleHeaders()

		view := m.View()
		if strings.Contains(view, "more header(s)") {
			t.Errorf("%d headers on a pane with room for them reported as dropped:\n%s", n, view)
		}
		for i := 0; i < n; i++ {
			if !strings.Contains(view, fmt.Sprintf("X-H%d", i)) {
				t.Errorf("%d headers: X-H%d is missing:\n%s", n, i, view)
			}
		}
	}
}
