package theme

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// NamedTheme is a selectable theme: a stable name, a human label, and the two
// variants the mode toggle flips between. The JSON shape is exactly this
// struct: {name, label, dark:{...tokens}, light:{...tokens}}.
type NamedTheme struct {
	Name  string  `json:"name"`
	Label string  `json:"label"`
	Dark  Palette `json:"dark"`
	Light Palette `json:"light"`
}

//go:embed builtin/*.json
var builtinFS embed.FS

// builtinOrder fixes the picker's display order, with the default first. User
// themes sort after these.
var builtinOrder = []string{"retro-82", "event-horizon"}

// Library is the ordered set of available themes plus any warnings from files
// that failed to load.
type Library struct {
	Themes   []NamedTheme
	Warnings []string
}

// LoadLibrary returns the embedded builtins in builtinOrder, then any valid
// *.json themes in userDir sorted by name. A user theme whose name matches a
// builtin replaces it in place, so a drop-in can retune a shipped theme without
// adding a duplicate row to the picker. userDir may be empty or absent. A
// malformed file is recorded in Warnings and skipped; it never breaks the load,
// because a typo in a color file must not cost you the application.
func LoadLibrary(userDir string) Library {
	var lib Library
	index := map[string]int{}

	add := func(nt NamedTheme) {
		if i, ok := index[nt.Name]; ok {
			lib.Themes[i] = nt
			return
		}
		index[nt.Name] = len(lib.Themes)
		lib.Themes = append(lib.Themes, nt)
	}

	for _, name := range builtinOrder {
		data, err := builtinFS.ReadFile("builtin/" + name + ".json")
		if err != nil {
			lib.Warnings = append(lib.Warnings, fmt.Sprintf("builtin %s: %v", name, err))
			continue
		}
		var nt NamedTheme
		if err := json.Unmarshal(data, &nt); err != nil {
			lib.Warnings = append(lib.Warnings, fmt.Sprintf("builtin %s: %v", name, err))
			continue
		}
		add(nt)
	}

	if userDir == "" {
		return lib
	}
	entries, err := os.ReadDir(userDir)
	if err != nil {
		return lib
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(userDir, file))
		if err != nil {
			lib.Warnings = append(lib.Warnings, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		var nt NamedTheme
		if err := json.Unmarshal(data, &nt); err != nil {
			lib.Warnings = append(lib.Warnings, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		if nt.Name == "" {
			lib.Warnings = append(lib.Warnings, fmt.Sprintf("%s: no name", file))
			continue
		}
		if nt.Label == "" {
			nt.Label = nt.Name
		}
		add(nt)
	}
	return lib
}
