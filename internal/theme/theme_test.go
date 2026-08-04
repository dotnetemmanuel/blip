package theme

import "testing"

func namedFixture() NamedTheme {
	return NamedTheme{
		Name:  "fixture",
		Label: "Fixture",
		Dark: Palette{
			Base: "#000000", Surface: "#111111", Overlay: "#222222",
			Text: "#eeeeee", Muted: "#888888", Primary: "#ff00ff",
			Focus: "#00ffff", Info: "#0088ff", Success: "#00ff00",
			Warning: "#ffaa00", Danger: "#ff0000", Accent2: "#00ffaa",
			CodeBg: "#333333",
		},
		Light: Palette{
			Base: "#ffffff", Surface: "#eeeeee", Overlay: "#dddddd",
			Text: "#111111", Muted: "#777777", Primary: "#aa00aa",
			Focus: "#0088aa", Info: "#005588", Success: "#008800",
			Warning: "#aa6600", Danger: "#aa0000", Accent2: "#008866",
			CodeBg: "#cccccc",
		},
	}
}

func TestToggleFlipsTheMode(t *testing.T) {
	if got := Toggle(ModeDark); got != ModeLight {
		t.Fatalf("Toggle(dark) = %q, want %q", got, ModeLight)
	}
	if got := Toggle(ModeLight); got != ModeDark {
		t.Fatalf("Toggle(light) = %q, want %q", got, ModeDark)
	}
}

// Anything that is not light is dark, so a corrupted preferences file costs a
// variant rather than the application.
func TestAnUnrecognisedModeCollapsesToDark(t *testing.T) {
	if got := Toggle("purple"); got != ModeLight {
		t.Fatalf("Toggle(purple) = %q, want %q since purple normalises to dark", got, ModeLight)
	}
	if got := ResolveNamed(namedFixture(), "purple", Palette{}); string(got.Base) != "#000000" {
		t.Fatalf("Base = %q, want the dark variant", got.Base)
	}
}

func TestResolveNamedPicksTheRequestedVariant(t *testing.T) {
	dark := ResolveNamed(namedFixture(), ModeDark, Palette{})
	if string(dark.Base) != "#000000" {
		t.Fatalf("dark Base = %q, want #000000", dark.Base)
	}
	light := ResolveNamed(namedFixture(), ModeLight, Palette{})
	if string(light.Base) != "#ffffff" {
		t.Fatalf("light Base = %q, want #ffffff", light.Base)
	}
}

func TestAnOverrideTouchesOnlyTheTokensItSets(t *testing.T) {
	got := ResolveNamed(namedFixture(), ModeDark, Palette{Primary: "#123456"})
	if string(got.Primary) != "#123456" {
		t.Fatalf("Primary = %q, want the override", got.Primary)
	}
	if string(got.Focus) != "#00ffff" {
		t.Fatalf("Focus = %q, want the theme value untouched", got.Focus)
	}
}

// The override layers before fallbacks, not after. Overriding Danger on a theme
// with no Error must move Error too, or an override behaves differently from
// editing the theme file, which is the whole promise of an override.
func TestAnOverrideAppliesBeforeFallbacks(t *testing.T) {
	got := ResolveNamed(namedFixture(), ModeDark, Palette{Danger: "#abcdef"})
	if string(got.Error) != "#abcdef" {
		t.Fatalf("Error = %q, want it to follow the overridden Danger", got.Error)
	}
}

// An explicit Error in the theme is not clobbered by an overridden Danger,
// because the theme said something specific and the override did not contradict it.
func TestAnExplicitErrorSurvivesAnOverriddenDanger(t *testing.T) {
	nt := namedFixture()
	nt.Dark.Error = "#ff2447"
	got := ResolveNamed(nt, ModeDark, Palette{Danger: "#abcdef"})
	if string(got.Error) != "#ff2447" {
		t.Fatalf("Error = %q, want the theme value %q", got.Error, "#ff2447")
	}
}

func TestResolveWithoutANamedThemeUsesTheOverrideAlone(t *testing.T) {
	got := Resolve(ModeDark, Palette{Base: "#010203", Text: "#040506"})
	if string(got.Base) != "#010203" {
		t.Fatalf("Base = %q, want the override", got.Base)
	}
	if string(got.Line) != "#040506" {
		t.Fatalf("Line = %q, want it to fall back to the overridden Text", got.Line)
	}
}
