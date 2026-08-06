package ui

import (
	"context"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dotnetemmanuel/blip/internal/config"
	"github.com/dotnetemmanuel/blip/internal/request"
)

// SendFunc performs a request the form resolved. The command layer binds it, so
// this package never holds a credential source: authentication is resolved
// inside the call, the first time something is actually sent, and browsing
// therefore never reaches for a vault.
type SendFunc func(ctx context.Context, env *config.Environment, req *http.Request) (*request.Response, error)

// sentMsg is the outcome of one send: a response, or the reason there is none.
type sentMsg struct {
	method  string
	resp    *request.Response
	elapsed time.Duration
	err     error
}

// sendCmd performs the request off the update loop. The clock starts here rather
// than in the caller, so the timing shown is the request and not the keystroke
// that led to it.
func sendCmd(ctx context.Context, send SendFunc, env *config.Environment, req *http.Request) tea.Cmd {
	if send == nil || req == nil {
		return nil
	}
	method := req.Method
	return func() tea.Msg {
		started := time.Now()
		resp, err := send(ctx, env, req)
		return sentMsg{method: method, resp: resp, elapsed: time.Since(started), err: err}
	}
}
