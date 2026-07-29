// Package safety decides whether a request is allowed to leave the machine.
package safety

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// Confirmer asks the user to approve a mutation. It is only ever called when
// stdin is a terminal.
type Confirmer func(prompt string) (bool, error)

// Gate applies the three safety rules.
type Gate struct {
	Readonly    bool
	Yes         bool
	Interactive bool
	DryRun      bool
	Confirm     Confirmer
}

// IsSafe reports whether a method only reads. Everything else is treated as a
// mutation, including verbs blip has never heard of.
func IsSafe(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func blocked(format string, args ...any) error {
	return output.WithCode(fmt.Errorf(format, args...), output.ExitBlocked)
}

// Check runs the rules in order: readonly first, because no flag may override it.
func (g Gate) Check(method, url, envName string) error {
	method = strings.ToUpper(method)
	if IsSafe(method) {
		return nil
	}

	if g.Readonly {
		return blocked("environment %q is readonly, so %s is refused; no flag overrides this", envName, method)
	}
	// A dry run sends nothing, so confirming it would be theatre. readonly still
	// applies above: that rule is about what the environment permits at all.
	if g.Yes || g.DryRun {
		return nil
	}
	if !g.Interactive {
		return blocked("%s %s in %q is a mutation and needs --yes (blip never prompts when stdin is not a terminal)",
			method, url, envName)
	}
	if g.Confirm == nil {
		return blocked("%s %s in %q is a mutation and needs --yes", method, url, envName)
	}

	ok, err := g.Confirm(fmt.Sprintf("%s %s in environment %s", method, url, envName))
	if err != nil {
		return blocked("reading confirmation: %v", err)
	}
	if !ok {
		return blocked("declined")
	}
	return nil
}
