package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/theme"
)

// paneState is which of Read, Fill and Send the right pane is in; only Read exists so far.
type paneState string

const stateRead paneState = "read"

// detailModel renders everything blip knows about the selected operation.
type detailModel struct {
	theme theme.Theme
	width int
	state paneState

	op *build.Operation

	mdRenderer *glamour.TermRenderer
	mdWidth    int
	mdMuted    bool

	descRendered string
}

func newDetailModel(t theme.Theme) detailModel {
	return detailModel{theme: t, state: stateRead}
}

// SetOperation loads the operation to describe; a nil operation clears the pane instead of panicking.
func (m *detailModel) SetOperation(op *build.Operation) {
	m.op = op
	m.refreshDescription()
}

// SetWidth records the pane width, which is also the column the description wraps to.
func (m *detailModel) SetWidth(width int) {
	if width == m.width {
		return
	}
	m.width = width
	m.refreshDescription()
}

func (m detailModel) View() string {
	if m.op == nil {
		return styled(m.theme, m.width, m.theme.Muted).Render("select an operation to see its details")
	}
	if m.state != stateRead {
		return styled(m.theme, m.width, m.theme.Error).Render(fmt.Sprintf("blip: unhandled pane state %q", m.state))
	}
	return wrapped(m.theme, m.width).Render(m.viewRead())
}

// readPalette collapses every role to Muted for a deprecated operation, so nothing about it reads as safe.
type readPalette struct {
	badge, heading, text, muted, accent, warn, codeBg lipgloss.Color
}

func (m detailModel) palette() readPalette {
	t := m.theme
	if m.op.Deprecated {
		return readPalette{badge: t.Muted, heading: t.Muted, text: t.Muted, muted: t.Muted, accent: t.Muted, warn: t.Muted, codeBg: t.CodeBg}
	}
	return readPalette{
		badge:   methodColor(t, m.op.Method),
		heading: t.Primary,
		text:    t.Text,
		muted:   t.Muted,
		accent:  t.Info,
		warn:    t.Warning,
		codeBg:  t.CodeBg,
	}
}

func (m detailModel) viewRead() string {
	op := m.op
	p := m.palette()

	var b strings.Builder
	b.WriteString(m.renderHeader(op, p))

	if m.descRendered != "" {
		b.WriteString("\n\n")
		b.WriteString(m.descRendered)
	}

	for _, in := range []string{build.InPath, build.InQuery, build.InHeader} {
		params := op.ParamsIn(in)
		if len(params) == 0 {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(m.renderHeading(sectionTitle(in), p))
		b.WriteString("\n")
		b.WriteString(m.renderRows(paramRows(params), p))
	}

	if op.Body != nil {
		b.WriteString("\n\n")
		b.WriteString(m.renderBodyHeading(op.Body, p))
		if len(op.Body.Fields) > 0 {
			b.WriteString("\n")
			b.WriteString(m.renderRows(fieldRows(op.Body.Fields), p))
		}
	}

	if responses := op.Responses(); len(responses) > 0 {
		b.WriteString("\n\n")
		b.WriteString(m.renderHeading("Responses", p))
		b.WriteString("\n")
		b.WriteString(m.renderResponseRows(responses, p))
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m detailModel) renderHeader(op *build.Operation, p readPalette) string {
	badge := lipgloss.NewStyle().Foreground(p.badge).Render(padMethod(op.Method))
	path := lipgloss.NewStyle().Foreground(p.text).Render(op.Path)
	line := badge + " " + path
	if op.Deprecated {
		line += "  " + lipgloss.NewStyle().Foreground(p.muted).Render("deprecated")
	}
	return line
}

func (m detailModel) renderHeading(title string, p readPalette) string {
	return lipgloss.NewStyle().Foreground(p.heading).Bold(true).Render(title)
}

func (m detailModel) renderBodyHeading(body *build.Body, p readPalette) string {
	title := m.renderHeading("Body", p)
	parts := []string{body.ContentType}
	if body.Schema != "" {
		parts = append(parts, body.Schema)
	}
	if body.Required {
		parts = append(parts, "required")
	}
	return title + "  " + lipgloss.NewStyle().Foreground(p.muted).Render(strings.Join(parts, "  "))
}

// detailRow is one parameter or field row: name, type, enum values and the required marker.
type detailRow struct {
	name     string
	typ      string
	enum     []string
	required bool
}

func paramRows(params []build.Param) []detailRow {
	rows := make([]detailRow, len(params))
	for i, param := range params {
		rows[i] = detailRow{name: param.Name, typ: arrayLabel(param.Type, param.ItemType), enum: param.Enum, required: param.Required}
	}
	return rows
}

func fieldRows(fields []build.Field) []detailRow {
	rows := make([]detailRow, len(fields))
	for i, f := range fields {
		rows[i] = detailRow{name: f.Name, typ: f.Type, enum: f.Enum, required: f.Required}
	}
	return rows
}

// arrayLabel spells out an array's element type instead of just "array".
func arrayLabel(t, itemType string) string {
	if t == build.TypeArray && itemType != "" {
		return "array<" + itemType + ">"
	}
	return t
}

func (m detailModel) renderRows(rows []detailRow, p readPalette) string {
	nameW, typeW := 0, 0
	for _, r := range rows {
		nameW = max(nameW, len(r.name))
		typeW = max(typeW, len(r.typ))
	}

	var b strings.Builder
	for _, r := range rows {
		name := lipgloss.NewStyle().Foreground(p.text).Render(padRight(r.name, nameW))
		typ := lipgloss.NewStyle().Foreground(p.muted).Render(padRight(r.typ, typeW))
		line := "  " + name + "  " + typ

		var extras []string
		if len(r.enum) > 0 {
			extras = append(extras, lipgloss.NewStyle().Foreground(p.accent).Render(strings.Join(r.enum, " | ")))
		}
		if r.required {
			extras = append(extras, lipgloss.NewStyle().Foreground(p.warn).Render("required"))
		}
		if len(extras) > 0 {
			line += "  " + strings.Join(extras, "  ")
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m detailModel) renderResponseRows(responses []build.Response, p readPalette) string {
	statusW := 0
	for _, r := range responses {
		statusW = max(statusW, len(r.Status))
	}

	var b strings.Builder
	for _, r := range responses {
		status := lipgloss.NewStyle().Foreground(p.text).Render(padRight(r.Status, statusW))
		line := "  " + status
		if label := responseLabel(r); label != "" {
			line += "  " + lipgloss.NewStyle().Foreground(p.muted).Render(label)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// responseLabel prefers the schema name over the bare resolved type.
func responseLabel(r build.Response) string {
	if r.Schema != "" {
		return r.Schema
	}
	return arrayLabel(r.Type, r.ItemType)
}

func sectionTitle(in string) string {
	switch in {
	case build.InPath:
		return "Path"
	case build.InQuery:
		return "Query"
	case build.InHeader:
		return "Header"
	default:
		return in
	}
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// refreshDescription recomputes descRendered, the only place this package parses markdown syntax.
func (m *detailModel) refreshDescription() {
	if m.op == nil {
		m.descRendered = ""
		return
	}
	desc := strings.TrimSpace(m.op.Description)
	if desc == "" {
		m.descRendered = ""
		return
	}

	m.ensureRenderer()
	if m.mdRenderer == nil {
		m.descRendered = desc
		return
	}
	out, err := m.mdRenderer.Render(desc)
	if err != nil {
		m.descRendered = desc
		return
	}
	m.descRendered = strings.TrimRight(out, "\n")
}

// ensureRenderer rebuilds the glamour renderer only when the width or the muted palette changed.
func (m *detailModel) ensureRenderer() {
	width := m.width
	if width <= 0 {
		width = 80
	}
	muted := m.op != nil && m.op.Deprecated
	if m.mdRenderer != nil && m.mdWidth == width && m.mdMuted == muted {
		return
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle(m.palette())),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(lipgloss.ColorProfile()),
	)
	if err != nil {
		m.mdRenderer = nil
		return
	}
	m.mdRenderer = r
	m.mdWidth = width
	m.mdMuted = muted
}

func markdownStyle(p readPalette) ansi.StyleConfig {
	str := func(c lipgloss.Color) *string { s := string(c); return &s }
	yes := func() *bool { b := true; return &b }
	prefixed := func(s string) ansi.StyleBlock { return ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: s}} }

	return ansi.StyleConfig{
		Document:    ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: str(p.text)}},
		Paragraph:   ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: str(p.text)}},
		Strong:      ansi.StylePrimitive{Bold: yes(), Color: str(p.heading)},
		Emph:        ansi.StylePrimitive{Italic: yes(), Color: str(p.text)},
		Code:        ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: str(p.accent), BackgroundColor: str(p.codeBg)}},
		Link:        ansi.StylePrimitive{Color: str(p.accent), Underline: yes()},
		Heading:     ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: str(p.heading), Bold: yes(), BlockSuffix: "\n"}},
		H1:          prefixed("# "),
		H2:          prefixed("## "),
		H3:          prefixed("### "),
		H4:          prefixed("#### "),
		H5:          prefixed("##### "),
		H6:          prefixed("###### "),
		List:        ansi.StyleList{LevelIndent: 2},
		Item:        ansi.StylePrimitive{BlockPrefix: "• ", Color: str(p.text)},
		Enumeration: ansi.StylePrimitive{BlockPrefix: ". ", Color: str(p.text)},
	}
}
