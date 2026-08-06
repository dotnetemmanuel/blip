package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

func formFor(op *build.Operation) formModel {
	m := newFormModel(theme.Theme{})
	m.SetOperation(op)
	return m
}

func fill(t *testing.T, m *formModel, key, value string) {
	t.Helper()
	for i := range m.fields {
		if m.fields[i].key == key {
			m.fields[i].value = value
			return
		}
	}
	t.Fatalf("no field %q; the form has %v", key, fieldKeys(*m))
}

func fieldKeys(m formModel) []string {
	keys := make([]string, len(m.fields))
	for i, f := range m.fields {
		keys[i] = f.key
	}
	return keys
}

func readBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading the request body: %v", err)
	}
	return body
}

func lineWidth(line string) int {
	return xansi.StringWidth(line)
}

func joinErrs(errs []error) string {
	parts := make([]string, len(errs))
	for i, err := range errs {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "; ")
}

func testEnv(t *testing.T, base string) *config.Environment {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parsing %q: %v", base, err)
	}
	return &config.Environment{Name: "local", BaseURL: u}
}

func TestFormOrdersFieldsPathQueryHeaderThenBody(t *testing.T) {
	op := &build.Operation{
		Method: "POST",
		Path:   "/orders/{orderId}/lines/{lineId}",
		Params: []build.Param{
			{Name: "X-Tenant", In: build.InHeader},
			{Name: "page", In: build.InQuery},
			{Name: "lineId", In: build.InPath, Required: true},
			{Name: "orderId", In: build.InPath, Required: true},
		},
		Body: &build.Body{ContentType: "application/json", Flat: true, Fields: []build.Field{{Name: "note"}}},
	}

	got := fieldKeys(formFor(op))

	want := []string{"path:orderId", "path:lineId", "query:page", "header:X-Tenant", "body:note"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestFormPrefillsDefaults(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "page", In: build.InQuery, Default: "1"},
		{Name: "size", In: build.InQuery},
	}}

	values := formFor(op).Values()

	if values["query:page"] != "1" {
		t.Errorf("page = %q, want the spec default 1", values["query:page"])
	}
	if values["query:size"] != "" {
		t.Errorf("size = %q, want it left empty", values["query:size"])
	}
}

func TestFormRefusesAClimbingPathValue(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders/{id}", Params: []build.Param{
		{Name: "id", In: build.InPath, Required: true},
	}}
	m := formFor(op)
	fill(t, &m, "path:id", "../admin")

	errs := m.Validate()

	if len(errs) == 0 {
		t.Fatal("a climbing path value was accepted, want it refused")
	}
	got := joinErrs(errs)
	if !strings.Contains(got, "id") || !strings.Contains(got, "..") {
		t.Errorf("errors = %q, want the parameter and the .. segment named", got)
	}
}

func TestFormRefusesAClimbInTheMiddleOfAValue(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders/{id}", Params: []build.Param{
		{Name: "id", In: build.InPath, Required: true},
	}}
	m := formFor(op)
	fill(t, &m, "path:id", "a/../../admin")

	if errs := m.Validate(); len(errs) == 0 {
		t.Fatal("a climb inside a longer value was accepted, want it refused")
	}
}

func TestFormAcceptsDotsInsideASegment(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders/{id}", Params: []build.Param{
		{Name: "id", In: build.InPath, Required: true},
	}}
	m := formFor(op)
	fill(t, &m, "path:id", "a..b")

	if errs := m.Validate(); len(errs) != 0 {
		t.Errorf("errors = %q, want a..b accepted", joinErrs(errs))
	}
}

func TestFormRefusesAMissingRequiredFieldAndNotAMissingOptional(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "page", In: build.InQuery, Required: true},
		{Name: "size", In: build.InQuery},
	}}
	m := formFor(op)

	errs := m.Validate()

	if len(errs) != 1 {
		t.Fatalf("errors = %q, want exactly the required one", joinErrs(errs))
	}
	if got := errs[0].Error(); !strings.Contains(got, "page") {
		t.Errorf("error = %q, want it to name page", got)
	}
}

// Required and nullable are different questions: one asks whether the field must
// be present, the other whether null is an answer.
func TestFormAcceptsAnEmptyRequiredNullableField(t *testing.T) {
	op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
		ContentType: "application/json", Flat: true,
		Fields: []build.Field{{Name: "note", Required: true, Nullable: true}},
	}}

	if errs := formFor(op).Validate(); len(errs) != 0 {
		t.Errorf("errors = %q, want a required nullable field to pass empty", joinErrs(errs))
	}
}

func TestFormSendsAnEmptyRequiredNullableFieldAsNull(t *testing.T) {
	op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
		ContentType: "application/json", Flat: true,
		Fields: []build.Field{{Name: "note", Required: true, Nullable: true}},
	}}
	m := formFor(op)

	req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(readBody(t, req), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	value, present := got["note"]
	if !present {
		t.Fatal("note was left out of the body, want it present as null")
	}
	if value != nil {
		t.Errorf("note = %v, want null", value)
	}
}

func TestFormRejectsAValueOutsideAnEnum(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "status", In: build.InQuery, Enum: []string{"open", "closed"}},
	}}
	m := formFor(op)
	fill(t, &m, "query:status", "shipped")

	errs := m.Validate()

	if len(errs) == 0 {
		t.Fatal("a value outside the enum was accepted, want it refused")
	}
	got := joinErrs(errs)
	if !strings.Contains(got, "open") || !strings.Contains(got, "closed") {
		t.Errorf("error = %q, want it to list the allowed values", got)
	}
}

func TestFormRejectsANonNumberForANumericParameter(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "page", In: build.InQuery, Type: build.TypeInteger},
	}}
	m := formFor(op)
	fill(t, &m, "query:page", "two")

	if errs := m.Validate(); len(errs) == 0 {
		t.Fatal("a non-number was accepted for an integer parameter, want it refused")
	}
}

func TestFormResolvesToTheRequestTheOperationDescribes(t *testing.T) {
	op := &build.Operation{
		Method: "POST",
		Path:   "/api/orders/{id}/lines",
		Params: []build.Param{
			{Name: "id", In: build.InPath, Required: true},
			{Name: "page", In: build.InQuery, Type: build.TypeInteger},
			{Name: "X-Tenant", In: build.InHeader},
		},
		Body: &build.Body{ContentType: "application/json", Flat: true, Fields: []build.Field{
			{Name: "sku"},
			{Name: "qty", Type: build.TypeInteger},
		}},
	}
	m := formFor(op)
	fill(t, &m, "path:id", "a/b")
	fill(t, &m, "query:page", "2")
	fill(t, &m, "header:X-Tenant", "acme")
	fill(t, &m, "body:sku", "SKU-1")
	fill(t, &m, "body:qty", "3")

	req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test/root"))
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}

	if want := "https://example.test/root/api/orders/a%2Fb/lines?page=2"; req.URL.String() != want {
		t.Errorf("url = %q, want %q", req.URL.String(), want)
	}
	if got := req.Header.Get("X-Tenant"); got != "acme" {
		t.Errorf("X-Tenant = %q, want acme", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	// qty is typed, so it must land as a JSON number rather than a quoted string.
	if got := string(readBody(t, req)); got != `{"qty":3,"sku":"SKU-1"}` {
		t.Errorf("body = %s, want qty as a number", got)
	}
}

func TestFormResolveRefusesBeforeSendingWhenAValueIsBad(t *testing.T) {
	op := &build.Operation{Method: "DELETE", Path: "/orders/{id}", Params: []build.Param{
		{Name: "id", In: build.InPath, Required: true},
	}}
	m := formFor(op)
	fill(t, &m, "path:id", "..")

	if _, err := m.Resolve(context.Background(), testEnv(t, "https://example.test")); err == nil {
		t.Fatal("Resolve built a request from a climbing value, want it refused")
	}
}

func TestFormSendsARawBodyForANestedSchema(t *testing.T) {
	op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
		ContentType: "application/json", Schema: "Order",
	}}
	m := formFor(op)
	if keys := fieldKeys(m); len(keys) != 1 || keys[0] != "body" {
		t.Fatalf("fields = %v, want one raw body field", keys)
	}
	fill(t, &m, "body", `{"nested":{"a":1}}`)

	req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}
	if got := string(readBody(t, req)); got != `{"nested":{"a":1}}` {
		t.Errorf("body = %s, want it sent verbatim", got)
	}
}

func TestFormTypingGoesIntoTheFocusedFieldOnly(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "page", In: build.InQuery},
		{Name: "size", In: build.InQuery},
	}}
	m := formFor(op)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0")})

	values := m.Values()
	if values["query:size"] != "20" {
		t.Errorf("size = %q, want 20", values["query:size"])
	}
	if values["query:page"] != "" {
		t.Errorf("page = %q, want it untouched", values["query:page"])
	}
}

// A lone space arrives as KeySpace, not KeyRunes, so a field that only handles
// runes silently drops it. The list pane had exactly this bug.
func TestFormTypingAcceptsASpace(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "q", In: build.InQuery},
	}}
	m := formFor(op)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

	if got := m.Values()["query:q"]; got != "a b" {
		t.Errorf("q = %q, want \"a b\"", got)
	}
}

func TestFormEditingReportsWhileTyping(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "q", In: build.InQuery},
	}}
	m := formFor(op)

	if m.Editing() {
		t.Error("the form reports editing before anything was opened")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.Editing() {
		t.Error("the form does not report editing after enter, so q would quit mid-field")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Editing() {
		t.Error("the form still reports editing after esc")
	}
}

// An enum is a picker, so enter must cycle it rather than open free text where a
// typed value could be anything.
func TestFormEnumCyclesInsteadOfTakingText(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "status", In: build.InQuery, Enum: []string{"open", "closed"}},
	}}
	m := formFor(op)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Editing() {
		t.Fatal("enter opened text entry on an enum field")
	}
	if got := m.Values()["query:status"]; got != "open" {
		t.Errorf("status = %q, want the first enum value", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.Values()["query:status"]; got != "closed" {
		t.Errorf("status = %q, want closed after a second press", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.Values()["query:status"]; got != "" {
		t.Errorf("status = %q, want the cycle to come back round to unset", got)
	}
}

func TestFormViewFitsTheHeightItWasGiven(t *testing.T) {
	params := make([]build.Param, 0, 30)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		params = append(params, build.Param{Name: name, In: build.InQuery})
	}
	m := formFor(&build.Operation{Method: "GET", Path: "/orders", Params: params})
	m.SetWidth(40)
	m.SetHeight(6)

	if got := strings.Count(m.View(), "\n") + 1; got > 6 {
		t.Errorf("the form drew %d lines, want at most 6", got)
	}
}

// A wrapped row desynchronizes the side-by-side join with the list pane. Width
// alone proves nothing here: wrapping also keeps every line inside the width, so
// the row count is what tells the two apart.
func TestFormViewClipsRatherThanWraps(t *testing.T) {
	m := formFor(&build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "averyveryverylongparameternamethatwillnotfit", In: build.InQuery, Type: build.TypeString, Required: true, Nullable: true},
	}})
	m.SetWidth(24)
	m.SetHeight(20)

	lines := strings.Split(m.View(), "\n")
	if len(lines) != 2 {
		t.Errorf("the form drew %d lines for one heading and one field, so a row wrapped:\n%s", len(lines), m.View())
	}
	for _, line := range lines {
		if got := lineWidth(line); got > 24 {
			t.Errorf("line %q is %d wide, want at most 24", line, got)
		}
	}
}

func TestFormScrollsToKeepTheCursorVisible(t *testing.T) {
	params := make([]build.Param, 0, 12)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		params = append(params, build.Param{Name: name, In: build.InQuery})
	}
	m := formFor(&build.Operation{Method: "GET", Path: "/orders", Params: params})
	m.SetWidth(40)
	m.SetHeight(5)

	for i := 0; i < 11; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}

	if !strings.Contains(m.View(), "l") {
		t.Errorf("the last field is off screen after moving onto it:\n%s", m.View())
	}
}

func TestFormNormalisesATypedValueTheWayTheCommandLineDoes(t *testing.T) {
	tests := []struct {
		name  string
		typ   string
		typed string
		want  string
	}{
		{name: "leading zeroes", typ: build.TypeInteger, typed: "007", want: "7"},
		{name: "explicit sign", typ: build.TypeInteger, typed: "+5", want: "5"},
		{name: "trailing zero", typ: build.TypeNumber, typed: "1.50", want: "1.5"},
		{name: "bare fraction", typ: build.TypeNumber, typed: ".5", want: "0.5"},
		{name: "shorthand true", typ: build.TypeBoolean, typed: "T", want: "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
				{Name: "v", In: build.InQuery, Type: tt.typ},
			}}
			m := formFor(op)
			fill(t, &m, "query:v", tt.typed)

			if errs := m.Validate(); len(errs) != 0 {
				t.Fatalf("errors = %q, want %q accepted", joinErrs(errs), tt.typed)
			}
			req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
			if err != nil {
				t.Fatalf("Resolve returned %v", err)
			}
			if got := req.URL.Query().Get("v"); got != tt.want {
				t.Errorf("v = %q, want %q", got, tt.want)
			}
		})
	}
}

// Anything Validate lets through must survive Resolve. A form that says a value
// is fine and then refuses to send it is worse than one that refused up front.
func TestFormValidateAgreesWithResolve(t *testing.T) {
	values := []string{"+5", "007", "1.50", ".5", "T", "Inf", "two", "", "0x10"}
	types := []string{build.TypeInteger, build.TypeNumber, build.TypeBoolean, build.TypeString}

	for _, typ := range types {
		for _, value := range values {
			op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
				ContentType: "application/json", Flat: true,
				Fields: []build.Field{{Name: "v", Type: typ}},
			}}
			m := formFor(op)
			fill(t, &m, "body:v", value)

			accepted := len(m.Validate()) == 0
			_, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
			if accepted && err != nil {
				t.Errorf("%s %q passed Validate but Resolve refused it: %v", typ, value, err)
			}
		}
	}
}

func TestFormSendsAListBodyFieldAsAListNotAString(t *testing.T) {
	op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
		ContentType: "application/json", Flat: true,
		Fields: []build.Field{{Name: "tags", Type: build.TypeArray}},
	}}
	m := formFor(op)
	fill(t, &m, "body:tags", `["a","b"]`)

	req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}
	if got := string(readBody(t, req)); got != `{"tags":["a","b"]}` {
		t.Errorf("body = %s, want tags as a real list", got)
	}
}

func TestFormRefusesAListBodyFieldThatIsNotAList(t *testing.T) {
	op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
		ContentType: "application/json", Flat: true,
		Fields: []build.Field{{Name: "tags", Type: build.TypeArray}},
	}}
	m := formFor(op)
	fill(t, &m, "body:tags", `"a"`)

	errs := m.Validate()
	if len(errs) == 0 {
		t.Fatal("a plain string was accepted for a list field, want it refused")
	}
	if got := joinErrs(errs); strings.Contains(got, "--field") {
		t.Errorf("error = %q, want it not to name a command-line flag the explorer has no such thing as", got)
	}
}

func TestFormSendsAListParameterAsRepeatedValues(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "tag", In: build.InQuery, Type: build.TypeArray, ItemType: build.TypeString},
	}}
	m := formFor(op)
	fill(t, &m, "query:tag", "a, b")

	req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}
	if got := req.URL.Query()["tag"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("tag = %v, want two separate values", got)
	}
}

// A name carrying a separator of its own must not be cut in the wrong place.
func TestFormBodyFieldNameWithASeparatorSurvives(t *testing.T) {
	for _, name := range []string{"a=b", "weird:"} {
		op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
			ContentType: "application/json", Flat: true,
			Fields: []build.Field{{Name: name, Type: build.TypeString}},
		}}
		m := formFor(op)
		fill(t, &m, "body:"+name, "c")

		req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
		if err != nil {
			t.Fatalf("%q: Resolve returned %v", name, err)
		}
		var got map[string]any
		if err := json.Unmarshal(readBody(t, req), &got); err != nil {
			t.Fatalf("%q: body is not JSON: %v", name, err)
		}
		if got[name] != "c" {
			t.Errorf("%q: body = %v, want the name and value kept whole", name, got)
		}
	}
}

// An empty required nullable parameter is the caller saying "present, no value",
// which a query string can only spell as name=.
func TestFormSendsAnEmptyRequiredNullableParameterAsAnEmptyValue(t *testing.T) {
	op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
		{Name: "cursor", In: build.InQuery, Required: true, Nullable: true},
		{Name: "size", In: build.InQuery},
	}}
	m := formFor(op)

	req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}
	if _, present := req.URL.Query()["cursor"]; !present {
		t.Errorf("query = %q, want cursor present with an empty value", req.URL.RawQuery)
	}
	if _, present := req.URL.Query()["size"]; present {
		t.Errorf("query = %q, want the empty optional left off entirely", req.URL.RawQuery)
	}
}

// The placeholder is the one render path with no field row to clip it.
func TestFormPlaceholderClipsRatherThanWraps(t *testing.T) {
	m := formFor(&build.Operation{Method: "GET", Path: "/healthz"})
	m.SetHeight(10)

	for _, width := range []int{8, 10, 16, 24} {
		m.SetWidth(width)
		if got := strings.Count(m.View(), "\n") + 1; got != 1 {
			t.Errorf("the empty-form placeholder drew %d lines at width %d:\n%s", got, width, m.View())
		}
	}

	empty := newFormModel(theme.Theme{})
	empty.SetWidth(10)
	if got := strings.Count(empty.View(), "\n") + 1; got != 1 {
		t.Errorf("the no-operation placeholder drew %d lines at width 10:\n%s", got, empty.View())
	}
}

// The parameter half of the same invariant TestFormValidateAgreesWithResolve
// covers for body fields: nothing Validate accepts may be refused on the way out.
func TestFormValidateAgreesWithResolveForParameters(t *testing.T) {
	values := []string{"+5", "007", "1.50", "T", "Inf", "two", " ", ",", "a, b", ""}
	types := []string{build.TypeInteger, build.TypeNumber, build.TypeBoolean, build.TypeString, build.TypeArray}

	for _, typ := range types {
		for _, value := range values {
			for _, required := range []bool{false, true} {
				op := &build.Operation{Method: "GET", Path: "/orders", Params: []build.Param{
					{Name: "v", In: build.InQuery, Type: typ, ItemType: build.TypeString, Required: required},
				}}
				m := formFor(op)
				fill(t, &m, "query:v", value)

				accepted := len(m.Validate()) == 0
				_, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
				if accepted && err != nil {
					t.Errorf("%s %q (required=%v) passed Validate but Resolve refused it: %v", typ, value, required, err)
				}
			}
		}
	}
}

// The explorer types a body field from the schema; the CLI requires key:=value
// and sends a string otherwise. The two differ deliberately, so pin it: a change
// that quietly makes the explorer send a string is a regression, not a tidy-up.
func TestFormTypesABodyFieldFromTheSchemaUnlikeTheCommandLine(t *testing.T) {
	op := &build.Operation{Method: "POST", Path: "/orders", Body: &build.Body{
		ContentType: "application/json", Flat: true,
		Fields: []build.Field{{Name: "qty", Type: build.TypeInteger}},
	}}
	m := formFor(op)
	fill(t, &m, "body:qty", "3")

	req, err := m.Resolve(context.Background(), testEnv(t, "https://example.test"))
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}
	if got := string(readBody(t, req)); got != `{"qty":3}` {
		t.Errorf("body = %s, want a number: the row says integer, so the box means integer", got)
	}
}
