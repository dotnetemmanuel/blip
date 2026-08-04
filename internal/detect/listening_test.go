package detect

import (
	"errors"
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
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: "/src/api"},
		{Port: 4000, PID: 2, Cwd: "/src/other"},
		{Port: 5000, PID: 3, Cwd: ""}, // unreadable cwd, owned by another user
	}}

	got, err := ListenersUnder(src, "/src/api")
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	want := []Listener{{Port: 3000, PID: 1, Cwd: "/src/api"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListenersUnder = %+v, want %+v", got, want)
	}
}

func TestListenersUnderRejectsPrefixThatIsNotAParent(t *testing.T) {
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: "/src/api-old"},
	}}

	got, err := ListenersUnder(src, "/src/api")
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListenersUnder = %+v, want none: /src/api-old is not under /src/api", got)
	}
}

func TestListenersUnderIncludesRepoRootItself(t *testing.T) {
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: "/src/api"},
	}}

	got, err := ListenersUnder(src, "/src/api")
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want the repo root process included", got)
	}
}

func TestListenersUnderIncludesNestedSubdirectory(t *testing.T) {
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: "/src/api/cmd/server"},
	}}

	got, err := ListenersUnder(src, "/src/api")
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want the nested process included", got)
	}
}

func TestListenersUnderExcludesParentDirectory(t *testing.T) {
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: "/src"},
	}}

	got, err := ListenersUnder(src, "/src/api")
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListenersUnder = %+v, want none: /src is a parent of /src/api, not under it", got)
	}
}

func TestListenersUnderDedupesSamePortAcrossFamilies(t *testing.T) {
	src := fakeSource{listeners: []Listener{
		{Port: 3000, PID: 1, Cwd: "/src/api"}, // IPv4
		{Port: 3000, PID: 1, Cwd: "/src/api"}, // IPv6, same process and port
	}}

	got, err := ListenersUnder(src, "/src/api")
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListenersUnder = %+v, want one deduped entry", got)
	}
}

func TestListenersUnderEmptySource(t *testing.T) {
	got, err := ListenersUnder(fakeSource{}, "/src/api")
	if err != nil {
		t.Fatalf("ListenersUnder: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListenersUnder = %+v, want none", got)
	}
}

func TestListenersUnderPropagatesSourceError(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := ListenersUnder(fakeSource{err: wantErr}, "/src/api")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListenersUnder: err = %v, want %v", err, wantErr)
	}
}
