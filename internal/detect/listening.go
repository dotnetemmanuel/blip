package detect

import (
	"path/filepath"
	"strings"
)

// Listener is one listening socket, with the working directory of its owning process.
type Listener struct {
	Port int
	PID  int
	Cwd  string
}

// SocketSource enumerates listening sockets; procSource implements it from /proc.
type SocketSource interface {
	Listening() ([]Listener, error)
}

// NewSocketSource is the real source, for callers outside this package.
func NewSocketSource() SocketSource { return procSource{} }

// ListenersUnder filters src's listeners to those under repoRoot, deduped by port.
func ListenersUnder(src SocketSource, repoRoot string) ([]Listener, error) {
	repoRoot, err := canonical(repoRoot)
	if err != nil {
		return nil, err
	}

	all, err := src.Listening()
	if err != nil {
		return nil, err
	}

	seenPorts := map[int]bool{}
	var out []Listener
	for _, l := range all {
		if !underRoot(repoRoot, l.Cwd) {
			continue
		}
		if seenPorts[l.Port] {
			continue
		}
		seenPorts[l.Port] = true
		out = append(out, l)
	}
	return out, nil
}

// canonical matches repoRoot's form to an already-canonical /proc cwd.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func underRoot(repoRoot, cwd string) bool {
	if cwd == "" {
		return false
	}
	cwd = filepath.Clean(cwd)
	return cwd == repoRoot || strings.HasPrefix(cwd, repoRoot+string(filepath.Separator))
}

type procSource struct{}
