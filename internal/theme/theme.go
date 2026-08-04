package theme

import "github.com/charmbracelet/lipgloss"

// The two variants a theme carries and the mode toggle flips between.
const (
	ModeDark  = "dark"
	ModeLight = "light"
)

// Theme is a resolved palette, typed for rendering. A UI holds one of these and
// therefore never holds a hex literal.
type Theme struct {
	Name    string
	Base    lipgloss.Color
	Surface lipgloss.Color
	Overlay lipgloss.Color
	Text    lipgloss.Color
	Line    lipgloss.Color
	Muted   lipgloss.Color
	Primary lipgloss.Color
	Focus   lipgloss.Color
	Info    lipgloss.Color
	Success lipgloss.Color
	Warning lipgloss.Color
	Danger  lipgloss.Color
	Error   lipgloss.Color
	Accent2 lipgloss.Color
	CodeBg  lipgloss.Color
	FocusBg lipgloss.Color
}

// NormalizeMode collapses anything that is not light to dark, so a corrupted
// preferences file costs a variant rather than the application.
func NormalizeMode(mode string) string {
	if mode == ModeLight {
		return ModeLight
	}
	return ModeDark
}

// Toggle returns the other variant.
func Toggle(mode string) string {
	if NormalizeMode(mode) == ModeLight {
		return ModeDark
	}
	return ModeLight
}

// ResolveNamed renders one variant of a named theme into a Theme, with the
// user's per-token override layered on first.
//
// Order matters: override, then fallbacks. Overriding Danger on a theme with no
// explicit Error moves Error too, so an override behaves exactly like editing
// the theme file.
func ResolveNamed(nt NamedTheme, mode string, override Palette) Theme {
	base := nt.Dark
	if NormalizeMode(mode) == ModeLight {
		base = nt.Light
	}
	return themeFrom(layer(base, override).Resolved())
}

// Resolve renders an override alone, for when no named theme is selected.
func Resolve(mode string, override Palette) Theme {
	return ResolveNamed(NamedTheme{}, mode, override)
}

// layer copies every non-empty token from over onto under.
func layer(under, over Palette) Palette {
	set := func(dst *string, src string) {
		if src != "" {
			*dst = src
		}
	}
	set(&under.Name, over.Name)
	set(&under.Base, over.Base)
	set(&under.Surface, over.Surface)
	set(&under.Overlay, over.Overlay)
	set(&under.Text, over.Text)
	set(&under.Line, over.Line)
	set(&under.Muted, over.Muted)
	set(&under.Primary, over.Primary)
	set(&under.Focus, over.Focus)
	set(&under.Info, over.Info)
	set(&under.Success, over.Success)
	set(&under.Warning, over.Warning)
	set(&under.Danger, over.Danger)
	set(&under.Error, over.Error)
	set(&under.Accent2, over.Accent2)
	set(&under.CodeBg, over.CodeBg)
	set(&under.FocusBg, over.FocusBg)
	return under
}

func themeFrom(p Palette) Theme {
	c := func(s string) lipgloss.Color { return lipgloss.Color(s) }
	return Theme{
		Name:    p.Name,
		Base:    c(p.Base),
		Surface: c(p.Surface),
		Overlay: c(p.Overlay),
		Text:    c(p.Text),
		Line:    c(p.Line),
		Muted:   c(p.Muted),
		Primary: c(p.Primary),
		Focus:   c(p.Focus),
		Info:    c(p.Info),
		Success: c(p.Success),
		Warning: c(p.Warning),
		Danger:  c(p.Danger),
		Error:   c(p.Error),
		Accent2: c(p.Accent2),
		CodeBg:  c(p.CodeBg),
		FocusBg: c(p.FocusBg),
	}
}
