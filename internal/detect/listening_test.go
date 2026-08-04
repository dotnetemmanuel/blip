package detect

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeSource struct {
	listeners []Listener
	err       error
}

func (f fakeSource) Listening() ([]Listener, error) {
	return f.listeners, f.err
}

func TestListenersUnderKeepsOnlyProcessesInsideRepoRoot(t *testing.T) {
	root := t.TempDir()
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: root},
		{Port: 4000, PID: 2, Cwd: filepath.Dir(root)},
		{Port: 5000, PID: 3, Cwd: ""}, // unreadable cwd, owned by another user
	}}

	got, err := ListenersUnder(src, root)
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	want := []Listener{{Port: 3000, PID: 1, Cwd: root}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListenersUnder = %+v, want %+v", got, want)
	}
}

func TestListenersUnderRejectsPrefixThatIsNotAParent(t *testing.T) {
	root := t.TempDir()
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: root + "-old"},
	}}

	got, err := ListenersUnder(src, root)
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListenersUnder = %+v, want none: %s-old is not under %s", got, root, root)
	}
}

func TestListenersUnderIncludesRepoRootItself(t *testing.T) {
	root := t.TempDir()
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: root},
	}}

	got, err := ListenersUnder(src, root)
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want the repo root process included", got)
	}
}

func TestListenersUnderIncludesNestedSubdirectory(t *testing.T) {
	root := t.TempDir()
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: filepath.Join(root, "cmd", "server")},
	}}

	got, err := ListenersUnder(src, root)
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want the nested process included", got)
	}
}

func TestListenersUnderExcludesParentDirectory(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "api")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: root},
	}}

	got, err := ListenersUnder(src, child)
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListenersUnder = %+v, want none: %s is a parent of %s, not under it", got, root, child)
	}
}

func TestListenersUnderResolvesSymlinkedRepoRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(base): %v", err)
	}
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// cwd is the resolved real path, matching what /proc/<pid>/cwd reports on Linux.
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: real},
	}}

	got, err := ListenersUnder(src, link)
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want the process under the symlinked repo root included", got)
	}
}

func TestListenersUnderResolvesRelativeRepoRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	t.Chdir(filepath.Dir(root))
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: root},
	}}

	got, err := ListenersUnder(src, filepath.Base(root))
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want the process under the relative repo root included", got)
	}
}

func TestListenersUnderErrorsOnRepoRootThatDoesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := ListenersUnder(fakeSource{}, missing)
	if err == nil {
		t.Fatalf("ListenersUnder(%s): want error, got nil", missing)
	}
}

func TestListenersUnderDedupesSamePortAcrossFamilies(t *testing.T) {
	root := t.TempDir()
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: root}, // IPv4 socket
		{Port: 3000, PID: 2, Cwd: root}, // IPv6 socket, different process, same port
	}}

	got, err := ListenersUnder(src, root)
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want one deduped entry", got)
	}
}

func TestListenersUnderEmptySource(t *testing.T) {
	got, err := ListenersUnder(fakeSource{}, t.TempDir())
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListenersUnder = %+v, want none", got)
	}
}

func TestListenersUnderPropagatesSourceError(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := ListenersUnder(fakeSource{err: wantErr}, t.TempDir())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListenersUnder: err = %v, want %v", err, wantErr)
	}
}
