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

// ListenersUnder keeps src's listeners owned by a process inside repoRoot, deduped by port.
func ListenersUnder(src SocketSource, repoRoot string) ([]Listener, error) {
	all, err := src.Listening()
	if err != nil {
		return nil, err
	}

	repoRoot = filepath.Clean(repoRoot)
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

func underRoot(repoRoot, cwd string) bool {
	if cwd == "" {
		return false
	}
	cwd = filepath.Clean(cwd)
	return cwd == repoRoot || strings.HasPrefix(cwd, repoRoot+string(filepath.Separator))
}

type procSource struct{}
