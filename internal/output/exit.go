// Package output renders responses and diagnostics, and owns the exit-code contract.
package output

import "errors"

// The exit-code contract. A caller branches on these, so they never change meaning.
const (
	ExitOK        = 0 // 2xx
	ExitInternal  = 1 // unexpected internal error
	ExitUsage     = 2 // bad flags, missing required param, unknown operation
	ExitConfig    = 3 // no config file, bad env, malformed TOML
	ExitAuth      = 4 // 401/403, or credential resolution failed
	ExitBlocked   = 5 // blocked by a safety rule
	ExitTransport = 6 // DNS, connection refused, timeout, TLS
	ExitClient    = 7 // 4xx other than 401/403
	ExitServer    = 8 // 5xx
)

type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }
func (e *codedError) ExitCode() int { return e.code }

// WithCode tags err with the exit code blip should terminate with.
func WithCode(err error, code int) error {
	if err == nil {
		return nil
	}
	return &codedError{code: code, err: err}
}

// Silent carries an exit code with nothing left to say, for when the failure has
// already been reported in full: a 404 body is the message, and "blip: error" on
// top of it is noise.
func Silent(code int) error {
	return &codedError{code: code, err: errors.New("")}
}

// ExitCodeFor reports the exit code for err. An untagged error is a bug in blip,
// not a predictable failure, so it maps to ExitInternal.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return ExitInternal
}
