package ui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiCodes.ReplaceAllString(s, "") }

func trimTrailingSpacePerLine(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file: %v (run: go test ./internal/ui -update)", err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// dotnet10Fixture is the real OpenAPI 3.1 capture, nullable-integer unions and all.
func dotnet10Fixture(t *testing.T) *build.API {
	t.Helper()
	data, err := os.ReadFile("../../testdata/dotnet10-minimal.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	api, err := build.Parse(data)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	return api
}

func findOp(t *testing.T, api *build.API, fullName string) *build.Operation {
	t.Helper()
	op := api.Find(fullName)
	if op == nil {
		t.Fatalf("no operation %q in fixture", fullName)
	}
	return op
}

func detailFor(t *testing.T, op *build.Operation) string {
	t.Helper()
	m := newDetailModel(theme.Theme{})
	m.SetOperation(op)
	m.SetWidth(100)
	return m.View()
}

// rowFor returns the row whose name column is exactly name, not just any line containing it.
func rowFor(rendered, name string) string {
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == name || strings.HasPrefix(trimmed, name+" ") {
			return line
		}
	}
	return ""
}

func TestNullablePageParamRendersAsIntegerNotUnknown(t *testing.T) {
	op := findOp(t, dotnet10Fixture(t), "ListOrders")
	got := detailFor(t, op)

	line := rowFor(got, "page")
	if line == "" {
		t.Fatalf("rendered detail has no row for the page parameter:\n%s", got)
	}
	if !strings.Contains(line, "integer") {
		t.Errorf("page row = %q, want it to contain \"integer\"", line)
	}
	if strings.Contains(line, "unknown") {
		t.Errorf("page row = %q, a nullable 3.1 union resolved to \"unknown\" instead of \"integer\"", line)
	}
}

func TestEnumQueryParamListsItsValues(t *testing.T) {
	op := findOp(t, dotnet10Fixture(t), "ListOrders")
	got := detailFor(t, op)

	line := rowFor(got, "status")
	if line == "" {
		t.Fatalf("rendered detail has no row for the status parameter:\n%s", got)
	}
	for _, want := range []string{"Open", "Shipped", "Cancelled"} {
		if !strings.Contains(line, want) {
			t.Errorf("status row = %q, missing enum value %q", line, want)
		}
	}
}

// quantity and note share a render, so only correct per-field required-ness passes this.
func TestRequiredBodyFieldCarriesMarkerAndOptionalFieldDoesNot(t *testing.T) {
	op := findOp(t, dotnet10Fixture(t), "CreateOrder")
	got := detailFor(t, op)

	required := rowFor(got, "quantity")
	if required == "" {
		t.Fatalf("rendered detail has no row for the quantity field:\n%s", got)
	}
	if !strings.Contains(required, "required") {
		t.Errorf("quantity row = %q, want it to carry the required marker", required)
	}
	if !strings.Contains(required, "integer") {
		t.Errorf("quantity row = %q, want its nullable 3.1 union resolved to integer", required)
	}

	optional := rowFor(got, "note")
	if optional == "" {
		t.Fatalf("rendered detail has no row for the note field:\n%s", got)
	}
	if strings.Contains(optional, "required") {
		t.Errorf("note row = %q, want no required marker: note is optional", optional)
	}
}

func TestResponseShapesAreListedIncludingOnesWithNoBody(t *testing.T) {
	op := findOp(t, dotnet10Fixture(t), "GetOrder")
	got := detailFor(t, op)

	ok := rowFor(got, "200")
	if ok == "" || !strings.Contains(ok, "Order") {
		t.Errorf("200 row = %q, want it to name the Order schema", ok)
	}
	notFound := rowFor(got, "404")
	if notFound == "" {
		t.Errorf("rendered detail has no row for 404:\n%s", got)
	}
}

func TestArrayQueryParamShowsItsElementType(t *testing.T) {
	op := findOp(t, dotnet10Fixture(t), "ListOrders")
	got := detailFor(t, op)

	line := rowFor(got, "tag")
	if line == "" {
		t.Fatalf("rendered detail has no row for the tag parameter:\n%s", got)
	}
	if !strings.Contains(line, "array") || !strings.Contains(line, "string") {
		t.Errorf("tag row = %q, want it to name array and its element type string", line)
	}
}

// The live twin proves Muted is conditional on Deprecated, not just the pane's only colour.
func TestDeprecatedOperationDetailRendersMutedThroughout(t *testing.T) {
	forceTrueColor(t)

	th := theme.Theme{Info: "#111111", Muted: "#999999", Text: "#555555", Primary: "#00ffff"}
	params := []build.Param{{Name: "x", In: build.InQuery, Type: build.TypeString}}
	live := &build.Operation{Method: "GET", Path: "/x", Params: params}
	dead := &build.Operation{Method: "GET", Path: "/x", Params: params, Deprecated: true}

	m := newDetailModel(th)
	m.SetOperation(live)
	liveView := m.View()
	m.SetOperation(dead)
	deadView := m.View()

	liveBadge := lipgloss.NewStyle().Foreground(th.Info).Render(padMethod("GET"))
	mutedBadge := lipgloss.NewStyle().Foreground(th.Muted).Render(padMethod("GET"))
	if !strings.Contains(liveView, liveBadge) {
		t.Fatalf("live render = %q, want the Info-coloured GET badge %q", liveView, liveBadge)
	}
	if !strings.Contains(deadView, mutedBadge) {
		t.Errorf("deprecated render = %q, want the Muted-coloured GET badge %q", deadView, mutedBadge)
	}
	if strings.Contains(deadView, liveBadge) {
		t.Errorf("deprecated render = %q, still carries the Info badge; nothing about it should read as safe", deadView)
	}

	liveHeading := lipgloss.NewStyle().Foreground(th.Primary).Bold(true).Render("Query")
	mutedHeading := lipgloss.NewStyle().Foreground(th.Muted).Bold(true).Render("Query")
	if !strings.Contains(liveView, liveHeading) {
		t.Fatalf("live render = %q, want the Primary-coloured Query heading %q", liveView, liveHeading)
	}
	if !strings.Contains(deadView, mutedHeading) {
		t.Errorf("deprecated render = %q, want the Muted-coloured Query heading %q", deadView, mutedHeading)
	}
}

func TestDescriptionRendersAsMarkdownNotLiteralSyntax(t *testing.T) {
	op := &build.Operation{
		Method:      "GET",
		Path:        "/x",
		Description: "Reads the **current** value from a `cache` entry.",
	}
	got := detailFor(t, op)

	if strings.Contains(got, "**") {
		t.Errorf("rendered detail still contains literal ** :\n%s", got)
	}
	if strings.Contains(got, "`cache`") {
		t.Errorf("rendered detail still contains a literal backtick around cache:\n%s", got)
	}
	if !strings.Contains(got, "current") || !strings.Contains(got, "cache") {
		t.Errorf("rendered detail is missing the markdown source's own words:\n%s", got)
	}
}

func TestNilOperationDoesNotPanicAndSaysSoInWords(t *testing.T) {
	m := newDetailModel(theme.Theme{})
	got := m.View()
	if strings.TrimSpace(got) == "" {
		t.Fatal("View() with no operation set is blank; want it to say something")
	}
}

func TestUnhandledPaneStateAnnouncesItselfRatherThanBlank(t *testing.T) {
	m := detailModel{theme: theme.Theme{}, op: &build.Operation{Method: "GET", Path: "/x"}, state: paneState("bogus")}
	got := m.View()
	if strings.TrimSpace(got) == "" {
		t.Fatal("an unhandled pane state renders blank, want it to say something")
	}
	if !strings.Contains(got, "bogus") {
		t.Errorf("got = %q, want it to name the unhandled state", got)
	}
}

func longDescriptionOp() *build.Operation {
	return &build.Operation{Method: "GET", Path: "/x", Description: "A description with enough words in it that glamour actually has something to lay out and cache across renders."}
}

// View must never rebuild the renderer: bubbletea redraws after every message, so a rebuild here pays for a fresh goldmark parser and chroma highlighter on every keypress.
func TestMarkdownRendererIsNotRebuiltOnEveryView(t *testing.T) {
	m := newDetailModel(theme.Theme{})
	m.SetWidth(80)
	m.SetOperation(longDescriptionOp())

	built := m.mdRenderer
	if built == nil {
		t.Fatal("mdRenderer is nil after SetOperation with a non-empty description")
	}
	m.View()
	m.View()
	m.View()
	if m.mdRenderer != built {
		t.Fatal("View() rebuilt the markdown renderer; it must only rebuild on SetWidth or SetOperation")
	}
}

func TestMarkdownRendererIsReusedAcrossOperationsAtTheSameWidth(t *testing.T) {
	m := newDetailModel(theme.Theme{})
	m.SetWidth(80)
	m.SetOperation(&build.Operation{Method: "GET", Path: "/a", Description: "First description, long enough to matter for wrapping."})
	built := m.mdRenderer

	m.SetOperation(&build.Operation{Method: "GET", Path: "/b", Description: "Second description, also long enough to matter."})
	if m.mdRenderer != built {
		t.Fatal("SetOperation rebuilt the renderer even though the width and deprecated state did not change")
	}
}

func TestMarkdownRendererRebuildsWhenWidthChanges(t *testing.T) {
	m := newDetailModel(theme.Theme{})
	m.SetOperation(longDescriptionOp())
	m.SetWidth(80)
	built := m.mdRenderer

	m.SetWidth(120)
	if m.mdRenderer == built {
		t.Fatal("SetWidth did not rebuild the renderer, so the description would keep wrapping at the old width")
	}
}

func TestMarkdownListsAndHeadingsRenderStructurally(t *testing.T) {
	op := &build.Operation{
		Method:      "GET",
		Path:        "/x",
		Description: "## Notes\n\nRetry rules:\n\n- First retry\n- Second retry\n\n1. Step one\n2. Step two\n",
	}
	got := trimTrailingSpacePerLine(stripANSI(detailFor(t, op)))

	if !strings.Contains(got, "• First retry") || !strings.Contains(got, "• Second retry") {
		t.Errorf("rendered detail is missing bulleted list markers:\n%s", got)
	}
	if !strings.Contains(got, "1. Step one") || !strings.Contains(got, "2. Step two") {
		t.Errorf("rendered detail is missing numbered list markers:\n%s", got)
	}
	if strings.Contains(got, "1Step one") {
		t.Errorf("numbered list ran together with no separator:\n%s", got)
	}
	if !strings.Contains(got, "## Notes") {
		t.Errorf("rendered detail dropped the heading marker:\n%s", got)
	}
	if !strings.Contains(got, "## Notes\n\nRetry rules") {
		t.Errorf("heading is not separated from the paragraph that follows it:\n%s", got)
	}
}

// widgetSpec forces the description to wrap differently at 80 and 120 columns.
const widgetSpec = `{"openapi":"3.0.1","info":{"title":"Widgets","version":"1"},"paths":{
	"/api/widgets/{id}/publish":{"post":{
		"tags":["Widgets"],
		"operationId":"publishWidget",
		"summary":"Publish a widget",
		"description":"Publishes the widget to the given channel, retrying with backoff if the channel is **temporarily unavailable**, and records a ` + "`correlationId`" + ` on every attempt so support can trace what happened after the fact.",
		"parameters":[
			{"name":"id","in":"path","required":true,"schema":{"type":"string"}},
			{"name":"channel","in":"query","required":true,"description":"Delivery channel.","schema":{"type":"string","enum":["web","email","sms"]}},
			{"name":"note","in":"query","schema":{"type":"string"}}
		],
		"requestBody":{"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/PublishRequest"}}}},
		"responses":{
			"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/WidgetResponse"}}}},
			"400":{"description":"Bad Request","content":{"application/json":{"schema":{"$ref":"#/components/schemas/ValidationProblem"}}}},
			"404":{"description":"Not Found"}
		}
	}}},
	"components":{"schemas":{
		"PublishRequest":{"type":"object","required":["title"],"properties":{
			"title":{"type":"string"},
			"tags":{"type":"array","items":{"type":"string"}}
		}},
		"WidgetResponse":{"type":"object","properties":{"id":{"type":"string"}}},
		"ValidationProblem":{"type":"object","properties":{"detail":{"type":"string"}}}
	}}}`

func widgetOp(t *testing.T) *build.Operation {
	t.Helper()
	return findOp(t, mustParse(t, widgetSpec), "publishWidget")
}

func TestGoldenDetailAt80Columns(t *testing.T) {
	forceTrueColor(t)
	m := newDetailModel(builtinDetailTheme())
	m.SetOperation(widgetOp(t))
	m.SetWidth(80)
	golden(t, "detail-80.txt", m.View()+"\n")
}

func TestGoldenDetailAt120Columns(t *testing.T) {
	forceTrueColor(t)
	m := newDetailModel(builtinDetailTheme())
	m.SetOperation(widgetOp(t))
	m.SetWidth(120)
	golden(t, "detail-120.txt", m.View()+"\n")
}

// builtinDetailTheme is a fixed palette so the goldens do not move if a real theme is retuned.
func builtinDetailTheme() theme.Theme {
	return theme.Theme{
		Text:    "#e6e6e6",
		Muted:   "#8a8a8a",
		Primary: "#7aa2f7",
		Info:    "#61afef",
		Warning: "#e5c07b",
		Success: "#98c379",
		Danger:  "#e06c75",
		CodeBg:  "#2a2e3a",
	}
}
