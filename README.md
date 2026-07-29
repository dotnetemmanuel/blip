# blip

Turn any HTTP API with an OpenAPI spec into a usable CLI. A single statically-linked Go
binary, with per-repo configuration committed alongside your code and credentials kept
well outside it.

blip is built for AI coding agents first. An agent debugging a backend needs to call that
backend, the way a browser extension lets an agent see a running UI. Humans are the
secondary consumer, and the tool doubles as a smoke-test harness.

> **Status: in development.** The repo is at milestone M0 (skeleton). Nothing below is
> usable yet. See [Milestones](#milestones) for what exists and what does not.

## Why it looks like this

Because the primary caller is a program, not a person:

- **Machine-parseable by default.** JSON on stdout, every diagnostic on stderr. Always.
- **Meaningful exit codes**, so a caller can branch without parsing prose.
- **Cheap discovery.** One command dumps the whole API surface compactly, instead of
  twenty `--help` invocations.
- **Deliberate mutations.** Writing to an API takes an explicit, visible step, and an
  agent can never satisfy that step by answering a prompt it cannot see.

## Install

```sh
make install    # builds and installs to ~/.local/bin/blip
```

Install once, per machine. Not per repo.

## Configure

Two files, and the split between them is the point.

### `.blip.toml`, committed at your repo root

```toml
name = "orders"
default_env = "dev"

[env.dev]
base_url = "https://localhost:7284"
spec_url = "/openapi/v1.json"    # optional, relative resolves against base_url
insecure = true                  # skip TLS verify, local hosts only
auth     = "orders-dev"          # names a profile in credentials.toml
timeout  = "30s"

[env.prod]
base_url = "https://api.example.internal"
auth     = "orders-prod"
readonly = true                  # hard-blocks every non-GET/HEAD/OPTIONS

# Optional. Hand-declared routes, for APIs with no spec at all.
[[route]]
name   = "health"
method = "GET"
path   = "/healthz"
```

blip walks up from the current directory looking for `.blip.toml`, and the first hit wins.
`blip.toml` without the leading dot is accepted too, if you prefer it visible.

`insecure = true` is rejected unless the host resolves to `localhost`, `127.0.0.1` or
`::1`. Skipping TLS verification against a real host is not something a committed file
should be able to arrange.

### `~/.config/blip/credentials.toml`, never committed, mode `0600`

```toml
[orders-dev]
type  = "bearer"
token = "eyJ..."

[orders-prod]
type                  = "oauth2_cc"
token_url             = "https://id.example.internal/connect/token"
client_id             = "blip"
client_secret_command = "pass show work/orders/prod"
scope                 = "orders.read orders.write"

[legacy]
type          = "header"
header        = "X-Api-Key"
value_command = "op read op://work/legacy/key"
```

Auth types: `none`, `bearer`, `header`, `basic`, `oauth2_cc`.

Every secret field has a `_command` variant (`token_command`, `client_secret_command`,
`value_command`, `password_command`). blip runs the command and uses its trimmed stdout.
That buys 1Password, `pass`, gopass, Vault and everything else at once, with no per-vault
integration to maintain. `${ENV_VAR}` interpolation works in value fields too.

If the credentials file has any mode other than `0600`, blip refuses to read it and says so.

## Use

```
blip <group> <operation> [args] [flags]   # generated from the spec
blip call <operationId> [args] [flags]    # stable, bypasses the generated tree
blip raw <METHOD> <PATH> [flags]          # zero-spec escape hatch
blip describe [--compact] [--json]        # the whole API surface
blip envs                                 # environments and resolved base URLs
blip auth test                            # resolve credentials, print nothing secret
blip spec [--refresh] [--path]            # show or refresh the cached spec
blip version
```

Global flags: `--env`, `--profile`, `--config`, `--refresh`, `--offline`, `--timeout`,
`--output {json|raw|status}`, `--include-headers`, `--verbose`, `--dry-run`, `--yes`.

`blip describe --compact` is the one to reach for first. One dense line per operation:

```
orders list      GET   /api/orders              ?status,?page,?pageSize
orders get       GET   /api/orders/{id}         id
orders create    POST  /api/orders              body: CreateOrderRequest
orders cancel    POST  /api/orders/{id}/cancel  id  body: CancelReason
```

`--dry-run` prints the fully resolved request, secrets redacted, and sends nothing. It
works for every command including mutations, and it is how a caller checks itself before
acting.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | 2xx |
| 1 | Unexpected internal error |
| 2 | Usage error (bad flags, missing required param, unknown operation) |
| 3 | Config error |
| 4 | Auth error (401/403, or credential resolution failed) |
| 5 | Blocked by a safety rule |
| 6 | Transport error (DNS, connection refused, timeout, TLS) |
| 7 | 4xx other than 401/403 |
| 8 | 5xx |

A non-2xx still prints the response body to stdout, with a one-line summary on stderr.
The API's own error payload is usually the whole answer, so blip does not swallow it.

### Safety

Three rules, small enough to be obviously correct:

1. `readonly = true` on an environment blocks every method except GET, HEAD and OPTIONS.
   No flag overrides it. Exit 5.
2. A mutating verb with no TTY requires `--yes`. Without it, exit 5. blip never prompts
   when stdin is not a terminal.
3. With a TTY, mutating verbs confirm first, showing method, resolved URL and environment.
   `--yes` skips the prompt.

`Authorization`, `Cookie`, `Set-Cookie` and any configured secret header are redacted in
all verbose and dry-run output. A resolved secret is never logged, not even at `--verbose`.

## Specs

Point `spec_url` wherever your spec lives. With it absent, blip probes, in order:

1. `/openapi/v1.json`
2. `/swagger/v1/swagger.json`
3. `/openapi/v1.yaml`
4. `/swagger/v1/swagger.yaml`

The first two cover .NET minimal APIs, whether the spec comes from
`Microsoft.AspNetCore.OpenApi` or Swashbuckle. Note that a docs UI such as Scalar or
Swagger UI renders a spec, it does not serve one; point blip at the JSON, not the page.

Specs are cached under `~/.cache/blip/<name>/` and revalidated with `If-None-Match`.
`--refresh` forces it, `--offline` forbids the network. If the backend is down but the
cache is warm, blip warns and carries on, because a backend being down is exactly when
you need this most.

Operations with no `operationId`, which is what minimal API endpoints declared without
`.WithName()` produce, get a deterministic name derived from method and path. blip warns
once naming them, so you know where to add `.WithName()` and `.WithTags()` upstream.

`XDG_CONFIG_HOME` and `XDG_CACHE_HOME` are respected when set.

## Using blip with Claude Code

Drop this into your project's `CLAUDE.md`:

```md
This repo has a blip config at `.blip.toml`. To explore the API, run
`blip describe --compact` once. To call an endpoint, use `blip <group> <op>` or
`blip call <operationId>`. The default environment is dev. Never pass `--env=prod`.
Before any POST/PUT/PATCH/DELETE, run the same command with `--dry-run` first, then
re-run it with `--yes`. Credentials live outside this repo and must not be added to it.
```

And pre-allow the tool so it does not prompt on every call:

```json
// .claude/settings.json
{ "permissions": { "allow": ["Bash(blip:*)"] } }
```

Permission rules prefix-match the whole command string, so a piped invocation such as
`blip orders list | jq .` needs each subcommand allowed independently. That is part of why
blip formats its own JSON rather than leaning on `jq`.

## Milestones

- [x] **M0** Skeleton: module, cobra root, `version`, Makefile, CI
- [ ] **M1** Config and credentials: discovery, env resolution, `_command`, `envs`, `auth test`
- [ ] **M2** Raw requests: `raw`, auth application, output modes, exit codes, safety rules
- [ ] **M3** Spec fetching: probe, ETag cache, `--refresh`, `--offline`, `spec`
- [ ] **M4** Generated command tree: name derivation, grouping, param binding, `call`
- [ ] **M5** `describe` and specless `[[route]]` entries
- [ ] **M6** Polish: docs, release artifacts

## What blip is not

Not a general HTTP client competing with curl, httpie or restish. No TUI, no REPL. No
response templating language and no `jq` reimplementation; it emits JSON and you pipe it.
No code generation, the command tree is always built from the spec at runtime. No plugins.

## License

MIT. See [LICENSE](LICENSE).
