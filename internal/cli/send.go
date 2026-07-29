package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/dotnetemmanuel/blip/internal/build"
	"github.com/dotnetemmanuel/blip/internal/output"
	"github.com/dotnetemmanuel/blip/internal/request"
	"github.com/dotnetemmanuel/blip/internal/safety"
)

// send applies safety, auth and output rules, then either prints the request
// (--dry-run) or performs it. op is nil for raw, which has no spec behind it.
func (rt *Runtime) send(ctx context.Context, req *request.Request, op *build.Operation) error {
	if err := output.ValidateMode(rt.Globals.Output); err != nil {
		return err
	}

	env, err := rt.Env()
	if err != nil {
		return err
	}

	httpReq, err := req.HTTPRequest(ctx, env.BaseURL)
	if err != nil {
		return err
	}

	gate := safety.Gate{
		Readonly:    env.Readonly,
		Yes:         rt.Globals.Yes,
		DryRun:      rt.Globals.DryRun,
		Interactive: rt.StdinIsTTY,
		Confirm:     rt.confirm,
	}
	if err := gate.Check(httpReq.Method, httpReq.URL.String(), env.Name); err != nil {
		return err
	}

	auth, err := rt.Authenticator(ctx)
	if err != nil {
		return err
	}
	if err := auth.Apply(ctx, httpReq); err != nil {
		return err
	}

	renderer := &output.Renderer{
		Mode:           rt.Globals.Output,
		Pretty:         rt.StdoutIsTTY,
		IncludeHeaders: rt.Globals.IncludeHeaders,
		Stdout:         rt.Stdout,
		Stderr:         rt.Stderr,
		Redactor:       output.NewRedactor(auth.Secrets()),
	}

	if rt.Globals.DryRun {
		return renderer.DryRun(httpReq.Method, httpReq.URL.String(), env.Name, httpReq.Header, req.Body)
	}

	rt.Verbosef("%s %s", httpReq.Method, renderer.Redactor.String(httpReq.URL.String()))
	for name, values := range renderer.Redactor.Header(httpReq.Header) {
		for _, v := range values {
			rt.Verbosef("> %s: %s", name, v)
		}
	}

	client, err := rt.Client()
	if err != nil {
		return err
	}
	resp, err := request.Do(client, httpReq)
	if err != nil {
		return err
	}

	if err := renderer.Response(resp.Status, resp.Header, resp.Body); err != nil {
		return err
	}

	if err := rt.validate(op, resp.Status, resp.Body); err != nil {
		return err
	}

	code := output.ExitCodeForStatus(resp.Status)
	if code == output.ExitOK {
		return nil
	}
	renderer.Summary(httpReq.Method, httpReq.URL.String(), resp.Status)
	return output.Silent(code)
}

// confirm asks before a mutation. The question goes to stderr so that stdout
// stays exactly what the caller asked for.
func (rt *Runtime) confirm(prompt string) (bool, error) {
	fmt.Fprintf(rt.Stderr, "blip: about to send %s\nProceed? [y/N] ", prompt)

	line, err := bufio.NewReader(rt.input()).ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// validate checks a response against the spec. Drift is a warning by default,
// because a mismatched schema should never stop you debugging; --strict turns it
// into a failure for the cases where the contract is the thing under test.
func (rt *Runtime) validate(op *build.Operation, status int, body []byte) error {
	if op == nil {
		return nil
	}
	err := op.ValidateResponse(status, body)
	if err == nil {
		return nil
	}
	if rt.Globals.Strict {
		return output.WithCode(err, output.ExitSchema)
	}
	rt.Warnf("%v", err)
	return nil
}
