// Package theme holds the semantic color palette the UI renders from.
package theme

// Palette is the set of semantic color tokens. Every token is a role, not a
// color: a consumer asks for Danger, never for red.
type Palette struct {
	Name    string `yaml:"name" json:"name"`
	Base    string `yaml:"base" json:"base"`       // app background
	Surface string `yaml:"surface" json:"surface"` // panel background
	Overlay string `yaml:"overlay" json:"overlay"` // dividers, inactive pane borders
	Text    string `yaml:"text" json:"text"`       // primary text
	Line    string `yaml:"line" json:"line"`       // borders drawn as rules, where they should read stronger than a divider
	Muted   string `yaml:"muted" json:"muted"`     // secondary text, inactive nodes
	Primary string `yaml:"primary" json:"primary"` // selection bar, "you are here"
	Focus   string `yaml:"focus" json:"focus"`     // focused pane border, section headers, spinners
	Info    string `yaml:"info" json:"info"`       // identifiers, links, safe reads
	Success string `yaml:"success" json:"success"` // passed, merged, created
	Warning string `yaml:"warning" json:"warning"` // pending, drifted, modified
	Danger  string `yaml:"danger" json:"danger"`   // a destructive action that is still valid, such as a DELETE
	Error   string `yaml:"error" json:"error"`     // something went wrong: a failure, a refusal, a 5xx
	Accent2 string `yaml:"accent2" json:"accent2"` // hashes, subtle emphasis
	CodeBg  string `yaml:"codeBg" json:"codeBg"`   // background for code blocks, decoupled from Overlay
	FocusBg string `yaml:"focusBg" json:"focusBg"` // background of the focused cursor bar
}

// Resolved returns a copy with every unset token filled from its fallback.
//
// Danger and Error are separate roles because both appear at once: a DELETE
// that returned 500 must not render the same as a DELETE that succeeded. A
// theme whose palette has no distinct crimson leaves Error unset and gets
// Danger, which is the old single-token behavior.
func (p Palette) Resolved() Palette {
	if p.Line == "" {
		p.Line = p.Text
	}
	if p.FocusBg == "" {
		p.FocusBg = p.Surface
	}
	if p.Error == "" {
		p.Error = p.Danger
	}
	return p
}
