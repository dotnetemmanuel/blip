package output

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeFor(t *testing.T) {
	base := errors.New("boom")

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, ExitOK},
		{"untagged errors are internal", base, ExitInternal},
		{"tagged error keeps its code", WithCode(base, ExitConfig), ExitConfig},
		{"tag survives wrapping", fmt.Errorf("while loading: %w", WithCode(base, ExitAuth)), ExitAuth},
		{"outermost tag wins", WithCode(WithCode(base, ExitAuth), ExitBlocked), ExitBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCodeFor(tt.err); got != tt.want {
				t.Errorf("ExitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestWithCodeOfNilIsNil(t *testing.T) {
	if err := WithCode(nil, ExitConfig); err != nil {
		t.Errorf("WithCode(nil, ...) = %v, want nil", err)
	}
}

func TestWithCodePreservesTheMessageAndTheCause(t *testing.T) {
	cause := errors.New("no .blip.toml found")
	err := WithCode(cause, ExitConfig)

	if err.Error() != cause.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), cause.Error())
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is could not reach the cause")
	}
}
