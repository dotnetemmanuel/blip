# blip

A single Go binary that turns any HTTP API with an OpenAPI spec into a CLI. The primary
consumer is a coding agent that needs to call a backend while debugging; humans are second.

## Working in this repo

- `make lint test build` before proposing anything. `make install` puts the binary in
  `~/.local/bin`.
- Unit tests cover config resolution, name derivation, param binding, redaction and the
  exit-code mapping. The HTTP layer is tested with `httptest.Server`, asserting on the
  outgoing request rather than the response. No test touches the network.
- `describe --compact` and `--dry-run` output are pinned by golden files under
  `internal/build/testdata` and `internal/cli/testdata`. They are contracts an agent parses.
  Regenerate deliberately with `make golden`, never to make a test pass.
- Spec fixtures live in `testdata/`. `dotnet9-minimal.json` deliberately has operations with
  no `operationId`, because real minimal APIs do.

## Invariants worth knowing before you change anything

- **stdout carries only the response body.** Every diagnostic, warning, prompt and header
  dump goes to stderr. A single stray `fmt.Println` breaks every caller that pipes output.
- **Exit codes are a contract.** The table lives in `internal/output/exit.go`. Errors carry
  their code by wrapping (`output.WithCode`), so a config error raised deep in the stack
  still exits 3. Do not classify errors by inspecting them at the top level.
- **Derived names must be deterministic.** Operations are sorted by path then method before
  names are derived, and collisions are numbered in that order. If you change the ordering,
  every generated command name in every user's scripts moves.
- **Credentials resolve lazily.** `--help`, `describe` and `envs` must never reach for a
  vault. Only `send` and `auth test` resolve them.
- **Secrets are redacted by value, not just by header name.** `internal/output.Redactor`
  replaces the literal secret wherever it appears, which is what catches it in a request body.
- **`readonly` is not overridable.** Not by `--yes`, not by `--dry-run`.

## Layout

```
internal/config    .blip.toml discovery, env resolution
internal/creds     credentials.toml, _command execution, ${ENV} interpolation
internal/auth      bearer, header, basic, oauth2_cc, token cache
internal/spec      fetch, probe, ETag cache
internal/build     spec to command tree, name derivation, describe rendering
internal/request   URL resolution, body assembly, execution
internal/output    rendering, redaction, exit codes
internal/safety    readonly and the TTY/--yes gate
internal/cli       command wiring and the runtime
```

## Using blip against this repo's own fixtures

There is no `.blip.toml` here; blip is the tool, not a consumer of one. To try it, point a
config at any local service and run `blip describe --compact`.
