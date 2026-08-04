package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func names(lib Library) []string {
	out := make([]string, 0, len(lib.Themes))
	for _, t := range lib.Themes {
		out = append(out, t.Name)
	}
	return out
}

func writeTheme(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinsLoadWithEventHorizonFirst(t *testing.T) {
	lib := LoadLibrary("")
	got := names(lib)
	want := []string{"event-horizon", "retro-82"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("builtin themes = %v, want %v", got, want)
	}
	if len(lib.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", lib.Warnings)
	}
}

func TestAbsentUserDirReturnsBuiltinsAlone(t *testing.T) {
	lib := LoadLibrary(filepath.Join(t.TempDir(), "does-not-exist"))
	if len(lib.Themes) != 2 {
		t.Fatalf("themes = %v, want the two builtins", names(lib))
	}
	if len(lib.Warnings) != 0 {
		t.Fatalf("a missing directory is not a warning: %v", lib.Warnings)
	}
}

// A drop-in named after a builtin retunes it rather than adding a second row
// with the same name to the picker.
func TestUserThemeReplacesABuiltinInPlace(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "retro-82.json",
		`{"name":"retro-82","label":"mine","dark":{"base":"#000000"},"light":{"base":"#ffffff"}}`)

	lib := LoadLibrary(dir)
	got := names(lib)
	if len(got) != 2 || got[0] != "event-horizon" || got[1] != "retro-82" {
		t.Fatalf("themes = %v, want the builtins with retro-82 replaced in place", got)
	}
	if lib.Themes[1].Label != "mine" {
		t.Fatalf("label = %q, want the drop-in to win", lib.Themes[1].Label)
	}
}

func TestUserThemeWithANewNameSortsAfterTheBuiltins(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "aardvark.json",
		`{"name":"aardvark","label":"Aardvark","dark":{"base":"#000000"},"light":{"base":"#ffffff"}}`)

	got := names(LoadLibrary(dir))
	want := []string{"event-horizon", "retro-82", "aardvark"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("themes = %v, want %v", got, want)
		}
	}
}

func TestAMalformedDropInIsWarnedAboutAndSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "broken.json", `{"name": "broken",`)

	lib := LoadLibrary(dir)
	if len(lib.Themes) != 2 {
		t.Fatalf("themes = %v, want the builtins alone", names(lib))
	}
	if len(lib.Warnings) != 1 || !strings.Contains(lib.Warnings[0], "broken.json") {
		t.Fatalf("warnings = %v, want one naming the file", lib.Warnings)
	}
}

// The semantic decision this module exists to carry: retro-82 has a distinct
// crimson because its primary, warning and danger are all orange and an error
// has nowhere to stand. event-horizon does not, because its danger already is
// crimson and its warning is peach.
func TestRetro82HasADistinctErrorAndEventHorizonDoesNot(t *testing.T) {
	lib := LoadLibrary("")
	byName := map[string]NamedTheme{}
	for _, t := range lib.Themes {
		byName[t.Name] = t
	}

	retro := byName["retro-82"].Dark.Resolved()
	if retro.Error == retro.Danger {
		t.Fatalf("retro-82 Error %q must differ from Danger %q", retro.Error, retro.Danger)
	}
	if retro.Error != "#ff2447" {
		t.Fatalf("retro-82 Error = %q, want the JetBrains ERROR_HINT crimson", retro.Error)
	}

	horizon := byName["event-horizon"].Dark.Resolved()
	if horizon.Error != horizon.Danger {
		t.Fatalf("event-horizon Error %q should fall back to Danger %q", horizon.Error, horizon.Danger)
	}
}

// Both variants of both builtins must define every structural token, or a theme
// renders with an invisible pane border and nobody can say why.
func TestEveryBuiltinVariantDefinesTheStructuralTokens(t *testing.T) {
	for _, nt := range LoadLibrary("").Themes {
		for variant, p := range map[string]Palette{"dark": nt.Dark, "light": nt.Light} {
			r := p.Resolved()
			for token, value := range map[string]string{
				"base": r.Base, "surface": r.Surface, "overlay": r.Overlay,
				"text": r.Text, "line": r.Line, "muted": r.Muted,
				"primary": r.Primary, "focus": r.Focus, "info": r.Info,
				"success": r.Success, "warning": r.Warning, "danger": r.Danger,
				"error": r.Error, "accent2": r.Accent2, "codeBg": r.CodeBg,
				"focusBg": r.FocusBg,
			} {
				if value == "" {
					t.Errorf("%s/%s leaves %s empty", nt.Name, variant, token)
				}
			}
		}
	}
}
