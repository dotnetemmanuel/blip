package theme

import "testing"

func TestLineFallsBackToText(t *testing.T) {
	p := Palette{Text: "#111111"}
	if got := p.Resolved().Line; got != "#111111" {
		t.Fatalf("Line = %q, want the Text color %q", got, "#111111")
	}
}

func TestFocusBgFallsBackToSurface(t *testing.T) {
	p := Palette{Surface: "#222222"}
	if got := p.Resolved().FocusBg; got != "#222222" {
		t.Fatalf("FocusBg = %q, want the Surface color %q", got, "#222222")
	}
}

func TestErrorFallsBackToDanger(t *testing.T) {
	p := Palette{Danger: "#e95678"}
	if got := p.Resolved().Error; got != "#e95678" {
		t.Fatalf("Error = %q, want the Danger color %q", got, "#e95678")
	}
}

func TestSetTokensSurviveResolution(t *testing.T) {
	p := Palette{
		Text:    "#111111",
		Surface: "#222222",
		Danger:  "#e95678",
		Line:    "#333333",
		FocusBg: "#444444",
		Error:   "#ff2447",
	}
	got := p.Resolved()
	if got.Line != "#333333" || got.FocusBg != "#444444" || got.Error != "#ff2447" {
		t.Fatalf("resolution overwrote explicit tokens: %+v", got)
	}
}

// Resolved returns a copy so a palette loaded once can be resolved repeatedly
// without the second call seeing the first call's fallbacks as explicit values.
func TestResolvedDoesNotMutateTheReceiver(t *testing.T) {
	p := Palette{Text: "#111111", Danger: "#e95678"}
	p.Resolved()
	if p.Line != "" || p.Error != "" {
		t.Fatalf("Resolved mutated the receiver: %+v", p)
	}
}
