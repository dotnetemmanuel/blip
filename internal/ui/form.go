package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

const stateFill paneState = "fill"

// inBody marks a field that belongs in the request body rather than in one of
// the parameter locations build names.
const inBody = "body"

// formField is one thing the request needs filled in. A field with an enum is a
// picker rather than free text, so nothing outside the set can be typed into it.
type formField struct {
	key      string
	name     string
	in       string
	typ      string
	itemType string
	desc     string
	enum     []string
	required bool
	nullable bool
	value    string
}

func (f formField) label() string {
	if f.in == inBody && f.name == "" {
		return "body"
	}
	return f.name
}

// typeLabel says what the field takes. A list parameter says how to type more
// than one, since the pane gives it a single box and nothing else would.
func (f formField) typeLabel() string {
	label := arrayLabel(f.typ, f.itemType)
	if f.typ == build.TypeArray && f.in != inBody {
		label += ", comma separated"
	}
	return label
}

// formModel is the Fill state: the values for one operation, and the rules for
// refusing a request before it reaches the network.
type formModel struct {
	theme  theme.Theme
	width  int
	height int

	op      *build.Operation
	fields  []formField
	cursor  int
	editing bool
}

func newFormModel(t theme.Theme) formModel {
	return formModel{theme: t}
}

// SetOperation rebuilds the fields for an operation, in the order the request
// uses them: path, query, header, then body. Defaults prefill.
func (m *formModel) SetOperation(op *build.Operation) {
	m.op = op
	m.fields = nil
	m.cursor = 0
	m.editing = false
	if op == nil {
		return
	}

	for _, p := range op.PathParams() {
		m.fields = append(m.fields, paramField(p))
	}
	for _, in := range []string{build.InQuery, build.InHeader} {
		for _, p := range op.ParamsIn(in) {
			m.fields = append(m.fields, paramField(p))
		}
	}

	if op.Body == nil {
		return
	}
	if !op.Body.Flat {
		// A nested schema has no fields to lay out, so the whole body is one
		// value the caller writes as JSON.
		m.fields = append(m.fields, formField{
			key:      inBody,
			in:       inBody,
			typ:      build.TypeObject,
			desc:     op.Body.Schema,
			required: op.Body.Required,
		})
		return
	}
	for _, f := range op.Body.Fields {
		m.fields = append(m.fields, formField{
			key:      inBody + ":" + f.Name,
			name:     f.Name,
			in:       inBody,
			typ:      f.Type,
			desc:     f.Description,
			enum:     f.Enum,
			required: f.Required,
			nullable: f.Nullable,
		})
	}
}

func paramField(p build.Param) formField {
	return formField{
		key:      p.In + ":" + p.Name,
		name:     p.Name,
		in:       p.In,
		typ:      p.Type,
		itemType: p.ItemType,
		desc:     p.Description,
		enum:     p.Enum,
		required: p.Required,
		nullable: p.Nullable,
		value:    p.Default,
	}
}

func (m *formModel) SetWidth(width int) { m.width = width }

func (m *formModel) SetHeight(height int) { m.height = height }

// Editing reports whether a field is taking free text right now, so the root
// model knows a plain "q" must type a letter instead of quitting.
func (m formModel) Editing() bool { return m.editing }

// Values is every field's current value, keyed by location and name so a query
// parameter and a path parameter of the same name stay apart.
func (m formModel) Values() map[string]string {
	values := make(map[string]string, len(m.fields))
	for _, f := range m.fields {
		values[f.key] = f.value
	}
	return values
}

// Validate reports everything wrong with the current values, in field order. It
// runs before anything is sent, so a climbing path value never reaches a server
// that might resolve it somewhere else.
func (m formModel) Validate() []error {
	var errs []error
	for _, f := range m.fields {
		if err := m.checkField(f); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (m formModel) checkField(f formField) error {
	if f.in == build.InPath {
		if segment, climbs := climbingSegment(f.value); climbs {
			return fmt.Errorf("%s cannot contain the segment %q, which would address a different endpoint", describeField(f), segment)
		}
	}

	if f.value == "" {
		if f.required && !f.nullable {
			return fmt.Errorf("%s needs a value", describeField(f))
		}
		return nil
	}

	if !inEnum(f.enum, f.value) {
		return fmt.Errorf("%s must be one of: %s", describeField(f), strings.Join(f.enum, ", "))
	}

	// The check is the encoding, run for its error. Asking the question a second
	// way is how a form comes to accept a value that then fails on the way out.
	if err := m.encodeCheck(f); err != nil {
		return fmt.Errorf("%s %s", describeField(f), err)
	}
	return nil
}

func (m formModel) encodeCheck(f formField) error {
	switch {
	case f.in != inBody:
		_, err := f.paramValues()
		return err
	case f.name != "":
		_, err := f.bodyValue()
		return err
	case m.bodyIsJSON():
		var parsed any
		if err := json.Unmarshal([]byte(f.value), &parsed); err != nil {
			return errors.New("must be valid JSON")
		}
	}
	return nil
}

func (m formModel) bodyIsJSON() bool {
	return m.op != nil && m.op.Body != nil && strings.Contains(m.op.Body.ContentType, "json")
}

// paramValues is what a parameter puts in the query string or the header. A list
// takes its values comma separated, matching the repeated flag the CLI offers.
func (f formField) paramValues() ([]string, error) {
	if f.value == "" {
		return []string{""}, nil
	}
	if f.typ != build.TypeArray {
		value, err := request.NormalizeScalar(f.typ, f.value)
		if err != nil {
			return nil, err
		}
		return []string{value}, nil
	}

	var out []string
	for _, part := range strings.Split(f.value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := request.NormalizeScalar(f.itemType, part)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, errors.New("needs at least one value between its commas")
	}
	return out, nil
}

// bodyValue is one field of a flat body. A field the schema types as anything but
// a string carries raw JSON, so a quantity the pane labels "integer" lands as a
// number rather than as a string that merely looks like one.
func (f formField) bodyValue() (request.FieldValue, error) {
	switch f.typ {
	case build.TypeArray, build.TypeObject:
		var parsed any
		if err := json.Unmarshal([]byte(f.value), &parsed); err != nil {
			return request.FieldValue{}, errors.New("must be valid JSON")
		}
		if _, isList := parsed.([]any); f.typ == build.TypeArray && !isList {
			return request.FieldValue{}, errors.New("must be a JSON list, such as [\"a\",\"b\"]")
		}
		if _, isObject := parsed.(map[string]any); f.typ == build.TypeObject && !isObject {
			return request.FieldValue{}, errors.New("must be a JSON object, such as {\"a\":1}")
		}
		return request.FieldValue{Name: f.name, Value: f.value, Raw: true}, nil

	case build.TypeInteger, build.TypeNumber, build.TypeBoolean:
		value, err := request.NormalizeScalar(f.typ, f.value)
		if err != nil {
			return request.FieldValue{}, err
		}
		return request.FieldValue{Name: f.name, Value: value, Raw: true}, nil

	default:
		return request.FieldValue{Name: f.name, Value: f.value}, nil
	}
}

func describeField(f formField) string {
	switch f.in {
	case inBody:
		if f.name == "" {
			return "the request body"
		}
		return "body field " + f.name
	default:
		return f.in + " parameter " + f.name
	}
}

// climbingSegment finds a .. segment in a value and names it. The value is still
// raw here, before any escaping, so a plain split finds exactly what request
// refuses once the URL is built, and no more: refusing more would mean the
// explorer turning down ids the command line accepts.
func climbingSegment(value string) (string, bool) {
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return segment, true
		}
	}
	return "", false
}

func inEnum(enum []string, value string) bool {
	if len(enum) == 0 {
		return true
	}
	for _, allowed := range enum {
		if allowed == value {
			return true
		}
	}
	return false
}

// Resolve turns the filled form into a request. Every value goes through the same
// assembler the CLI uses, so the explorer cannot send a shape the command line
// would have refused.
func (m formModel) Resolve(ctx context.Context, env *config.Environment) (*http.Request, error) {
	if m.op == nil {
		return nil, output.Usagef("no operation is selected")
	}
	if errs := m.Validate(); len(errs) > 0 {
		return nil, output.WithCode(errors.Join(errs...), output.ExitUsage)
	}
	if env == nil || env.BaseURL == nil {
		return nil, output.Configf("this target has no base URL, so there is nowhere to send the request")
	}

	values, err := m.operationValues()
	if err != nil {
		return nil, err
	}
	if missing := request.MissingFrom(m.op, values); missing.Any() {
		return nil, output.Usagef("%s", missingMessage(missing))
	}

	req, err := request.ForOperation(m.op, values)
	if err != nil {
		return nil, err
	}
	return req.HTTPRequest(ctx, env.BaseURL)
}

func missingMessage(missing request.Missing) string {
	var parts []string
	for _, p := range missing.Params {
		parts = append(parts, p.In+" parameter "+p.Name)
	}
	if missing.Body {
		parts = append(parts, "the request body")
	}
	return "still to fill in: " + strings.Join(parts, ", ")
}

func (m formModel) operationValues() (request.OperationValues, error) {
	values := request.OperationValues{Query: url.Values{}, Header: http.Header{}}
	var fields []request.FieldValue

	for _, f := range m.fields {
		switch f.in {
		case build.InPath:
			values.Path = append(values.Path, f.value)

		case build.InQuery, build.InHeader:
			if !m.sends(f) {
				continue
			}
			vs, err := f.paramValues()
			if err != nil {
				return values, output.Usagef("%s %s", describeField(f), err)
			}
			for _, v := range vs {
				if f.in == build.InQuery {
					values.Query.Add(f.name, v)
					continue
				}
				values.Header.Add(f.name, v)
			}

		case inBody:
			if f.name == "" {
				values.Body = []byte(f.value)
				continue
			}
			switch {
			case f.value == "" && f.required && f.nullable:
				fields = append(fields, request.FieldValue{Name: f.name, Value: "null", Raw: true})
			case f.value == "":
			default:
				fv, err := f.bodyValue()
				if err != nil {
					return values, output.Usagef("%s %s", describeField(f), err)
				}
				fields = append(fields, fv)
			}
		}
	}

	if len(fields) > 0 {
		body, err := request.BuildFields(fields)
		if err != nil {
			return values, err
		}
		values.Body = body
	}
	return values, nil
}

// sends decides whether a parameter goes on the wire at all. An empty optional
// is left off, but an empty required nullable is the caller saying "present, no
// value", which a query string can only spell as name=.
func (m formModel) sends(f formField) bool {
	return f.value != "" || (f.required && f.nullable)
}

func (m formModel) Update(msg tea.Msg) (formModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok || len(m.fields) == 0 {
		return m, nil
	}

	if m.editing {
		switch key.Type {
		case tea.KeyEsc, tea.KeyEnter:
			m.editing = false
		case tea.KeyBackspace:
			if r := []rune(m.fields[m.cursor].value); len(r) > 0 {
				m.fields[m.cursor].value = string(r[:len(r)-1])
			}
		case tea.KeyRunes:
			m.fields[m.cursor].value += string(key.Runes)
		case tea.KeySpace:
			m.fields[m.cursor].value += " "
		}
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		m.cursor = wrapIndex(m.cursor-1, len(m.fields))
	case "down", "j":
		m.cursor = wrapIndex(m.cursor+1, len(m.fields))
	case "enter", "right", "l":
		m.activate(1)
	case "left", "h":
		m.activate(-1)
	}
	return m, nil
}

// activate opens a field. A picker steps through its choices; anything else
// starts taking text.
func (m *formModel) activate(step int) {
	f := &m.fields[m.cursor]
	if len(f.enum) == 0 {
		if step > 0 {
			m.editing = true
		}
		return
	}
	choices := append([]string{""}, f.enum...)
	at := 0
	for i, c := range choices {
		if c == f.value {
			at = i
			break
		}
	}
	f.value = choices[wrapIndex(at+step, len(choices))]
}

func (m formModel) View() string {
	if m.op == nil {
		return styled(m.theme, m.width, m.theme.Muted).Render(clipToWidth("", "select an operation to fill in", m.width))
	}

	lines, focus := m.lines()
	return strings.Join(windowAround(lines, focus, m.height), "\n")
}

// lines renders every row and reports which one the cursor is on, so the window
// can be taken over the finished picture rather than over the fields.
func (m formModel) lines() (rendered []string, focus int) {
	focus = 0
	nameW := 0
	for _, f := range m.fields {
		nameW = max(nameW, len(f.label()))
	}

	section := ""
	for i, f := range m.fields {
		if f.in != section {
			section = f.in
			rendered = append(rendered, m.renderSection(f))
		}
		if i == m.cursor {
			focus = len(rendered)
		}
		rendered = append(rendered, m.renderField(f, nameW, i == m.cursor))
	}
	if len(rendered) == 0 {
		rendered = append(rendered, styled(m.theme, m.width, m.theme.Muted).Render(clipToWidth("", "nothing to fill in", m.width)))
	}
	return rendered, focus
}

func (m formModel) renderSection(f formField) string {
	title := sectionTitle(f.in)
	if f.in == inBody {
		title = "Body"
		if m.op.Body != nil && m.op.Body.ContentType != "" {
			title += "  " + m.op.Body.ContentType
		}
	}
	return lipgloss.NewStyle().Foreground(m.theme.Primary).Bold(true).
		Render(clipToWidth("", title, m.width))
}

func (m formModel) renderField(f formField, nameW int, selected bool) string {
	name := lipgloss.NewStyle().Foreground(m.theme.Text).Render(padRight(f.label(), nameW))
	prefix := "  " + name + "  "

	style := styled(m.theme, m.width, m.theme.Text)
	if selected {
		style = style.Background(m.theme.FocusBg)
	}
	return style.Render(clipToWidth(prefix, m.fieldValue(f, selected), m.width))
}

// fieldValue is the part of a row that shows what is in the field and what the
// field will accept, drawn as one string so clipping cannot cut it mid-marker.
func (m formModel) fieldValue(f formField, selected bool) string {
	var body string
	switch {
	case len(f.enum) > 0:
		body = "< " + orPlaceholder(f.value, "unset") + " >"
	case selected && m.editing:
		body = "[" + f.value + "▏]"
	default:
		body = "[" + orPlaceholder(f.value, " ") + "]"
	}

	var notes []string
	if label := f.typeLabel(); label != "" && len(f.enum) == 0 {
		notes = append(notes, label)
	}
	if f.required {
		notes = append(notes, "required")
	}
	if f.nullable {
		notes = append(notes, "nullable")
	}
	if len(notes) > 0 {
		body += "  " + strings.Join(notes, " ")
	}
	return body
}

func orPlaceholder(value, placeholder string) string {
	if value == "" {
		return placeholder
	}
	return value
}

// windowAround takes at most height lines, sliding to keep focus inside them.
// Clamping alone would put a field below the fold out of reach of the cursor.
func windowAround(lines []string, focus, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	start := focus - height/2
	if start > len(lines)-height {
		start = len(lines) - height
	}
	if start < 0 {
		start = 0
	}
	return lines[start : start+height]
}
