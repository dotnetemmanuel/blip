# blip

Turn any HTTP API with an OpenAPI spec into a usable CLI. A single statically-linked Go
binary, with per-repo configuration committed alongside your code and credentials kept
well outside it.

blip is built for AI coding agents first. An agent debugging a backend needs to call that
backend, the way a browser extension lets an agent see a running UI. Humans are the
secondary consumer, and the tool doubles as a smoke-test harness.

```
$ blip describe --compact
orders list                          GET     /api/orders                         ?status,?page,?pageSize,?tag
orders create                        POST    /api/orders                         body!:CreateOrderRequest
orders get                           GET     /api/orders/{id}                    id,@X-Tenant
orders delete-by-id                  DELETE  /api/orders/{id}                    id
orders cancel                        POST    /api/orders/{id}/cancel             id  body!:CancelReason

$ blip orders get 7f00-0101
{"id":"7f00-0101","status":"open","total":42.5}
```

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
git clone https://github.com/dotnetemmanuel/blip
cd blip
make install        # builds and installs to ~/.local/bin/blip
```

Install once, per machine. Not per repo. `make dist` cross-compiles for linux and darwin
on amd64 and arm64 into `dist/`.

## Walkthrough

Start with a service running locally. This example uses a .NET 10 minimal API on
`https://localhost:7284`, but anything that serves an OpenAPI document works.

**1. Write `.blip.toml` at your repo root and commit it.**

```toml
name = "orders"
default_env = "dev"

[env.dev]
base_url = "https://localhost:7284"
insecure = true            # dotnet dev-certs, localhost only
auth     = "orders-dev"
```

No `spec_url` is needed if the spec is at one of the usual places; blip probes them.

**2. Put the credential outside the repo**, in `~/.config/blip/credentials.toml` at mode
`0600`:

```toml
[orders-dev]
type  = "bearer"
token_command = "pass show work/orders/dev"
```

Check it resolves. Nothing secret is printed, only a fingerprint:

```
$ blip auth test
env       dev
base_url  https://localhost:7284
profile   orders-dev
resolved  bearer token sha256:4e1f8f15
```

**3. Look at the API.** This is the command to run first, and usually the only one you need
before making a call:

```
$ blip describe --compact
customers orders-get-by-customer-id  GET     /api/customers/{customerId}/orders  customerId
healthz get                          GET     /healthz
orders list                          GET     /api/orders                         ?status,?page,?pageSize,?tag
orders create                        POST    /api/orders                         body!:CreateOrderRequest
orders get                           GET     /api/orders/{id}                    id,@X-Tenant
orders delete-by-id                  DELETE  /api/orders/{id}                    id
orders cancel                        POST    /api/orders/{id}/cancel             id  body!:CancelReason
orders lines-get-by-id-by-line-id    GET     /api/orders/{id}/lines/{lineId}     id,lineId
```

Reading the notation: path parameters are bare and positional, `?name` is a query flag,
`@name` is a header flag, a trailing `*` means required, and `body:Schema` is a request
body (`body!:Schema` when the body is required).

Two of those names were derived, because the endpoints were declared without `.WithName()`
and so carry no `operationId`. blip says so once, on stderr:

```
blip: 4 operations have no operationId, so blip derived names for them: GET /api/customers/{customerId}/orders,
DELETE /api/orders/{id}, ... Add .WithName() and .WithTags() upstream to control them
```

**4. Read something.**

```
$ blip orders list --status open --page 2
{"items":[...],"page":2}

$ blip orders get 7f00-0101 --header-x-tenant acme
{"id":"7f00-0101","status":"open"}
```

**5. Check a mutation before making it.** `--dry-run` resolves everything, prints the
request with secrets redacted, and sends nothing:

```
$ blip orders create --field sku=A1 --field quantity:=2 --dry-run
POST https://localhost:7284/api/orders
env: dev
Accept: application/json
Authorization: Bearer <redacted>
Content-Type: application/json

{"quantity":2,"sku":"A1"}
```

`--field sku=A1` sends a string; `--field quantity:=2` sends raw JSON, which is how you
send a number, a boolean or null. Anything that is not a flat object uses
`--data @order.json`, `--data @-` or inline JSON.

**6. Make it.** A mutating verb needs `--yes` when there is no terminal, and prompts when
there is one:

```
$ blip orders create --field sku=A1 --field quantity:=2 --yes
{"id":"7f00-0102","status":"open"}
```

## Configure

Two files, and the split between them is the point. See
[`.blip.toml.example`](.blip.toml.example) for a commented template.

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

[env.staging]
base_url    = "https://staging.example.internal"
client_cert = "certs/client.pem" # mTLS; relative to this file
client_key  = "certs/client.key"

# Hand-declared routes, for an API with no spec at all.
[[route]]
name   = "health"
method = "GET"
path   = "/healthz"
```

blip walks up from the current directory looking for `.blip.toml`, and the first hit wins.
`blip.toml` without the leading dot is accepted too, if you prefer it visible. `--config`
overrides both.

Unknown keys are an error rather than being ignored, so a typo like `read_only` cannot
silently disable the guard you meant to switch on.

`insecure = true` is rejected unless the host is loopback, and it applies only to the API
itself. An `oauth2_cc` token endpoint on another host is always verified, because the
exemption was granted for a local development certificate, not for an identity provider.

A `[[route]]` with no `group` becomes a top-level command (`blip health`); give it a
`group` to nest it. Routes merge into the generated tree, and work with no spec at all.

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
`value_command`, `password_command`). blip runs the command through `sh` and uses its
trimmed stdout. That buys 1Password, `pass`, gopass, Vault and everything else at once,
with no per-vault integration to maintain. `${ENV_VAR}` interpolation works in value
fields too.

`oauth2_cc` tokens are cached at `~/.cache/blip/tokens/` at `0600` and refreshed within
60 seconds of expiry. The cache key includes the scope, so widening a scope cannot be
served from a narrower cached token.

If the credentials file has any mode other than `0600`, blip refuses to read it and says so.

## Use

```
blip <group> <operation> [args] [flags]   # generated from the spec
blip call <operationId> [args] [flags]    # stable, bypasses the generated tree
blip raw <METHOD> <PATH> [flags]          # zero-spec escape hatch
blip describe [--compact] [--json]        # the whole API surface
blip envs [--json]                        # environments and resolved base URLs
blip auth test                            # resolve credentials, print nothing secret
blip spec [--path] [--meta]               # show or refresh the cached spec
blip version
```

Global flags: `--env`, `--profile`, `--config`, `--refresh`, `--offline`, `--timeout`,
`--output {json|raw|status}`, `--include-headers`, `--verbose`, `--dry-run`, `--yes`,
`--strict`.

`blip call` addresses an operation by its `operationId`, or by `group-name` when the spec
gives no id. Those names do not move when tags or derivation change, so they are the safer
thing to put in a script.

`blip raw` applies the base URL, credentials, TLS settings and safety rules but needs no
spec. A path must start with `/`, and blip will not send your credentials to another host:
a path argument cannot escape the operation it names, a redirect that changes host is
refused rather than followed, and a `spec_url` on a different host is fetched without
credentials.

Generated `--help` carries the operation's summary, description and each parameter's
description straight from the spec.

### Output contract

- `--output json` (default): the response body on stdout, pretty-printed at a terminal and
  compact when piped.
- `--output raw`: body bytes, untouched.
- `--output status`: the status code alone.
- `--include-headers`: response headers on **stderr**, so stdout stays pipeable.

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
| 7 | 4xx other than 401/403, and any other non-2xx |
| 8 | 5xx |
| 9 | Response did not match the spec schema, under `--strict` |

A non-2xx still prints the response body to stdout, with a one-line summary on stderr.
The API's own error payload is usually the whole answer, so blip does not swallow it.

### Safety

Three rules, small enough to be obviously correct:

1. `readonly = true` on an environment blocks every method except GET, HEAD and OPTIONS.
   No flag overrides it, including `--dry-run`. Exit 5.
2. A mutating verb with no TTY requires `--yes`. Without it, exit 5. blip never prompts
   when stdin is not a terminal.
3. With a TTY, mutating verbs confirm first, showing method, resolved URL and environment.
   `--yes` skips the prompt.

`--dry-run` is exempt from rules 2 and 3, since it sends nothing. It still resolves
credentials, so the header it prints is the header that would be sent; for `oauth2_cc`
that means a real request to the token endpoint, though never to the API itself.

`Authorization`, `Proxy-Authorization`, `Cookie` and `Set-Cookie` are redacted in all
verbose and dry-run output, and every resolved secret is redacted by value wherever it
appears: in a request body, in a query string, in the summary line and in an error
message. Redaction is literal substring matching, so a secret that the API re-encodes
before echoing it back is not caught.

A parameter in the spec can never take over one of blip's own flags. A query parameter
called `dry-run` or `output` is exposed as `--query-dry-run` and `--query-output`, and the
rename is shown in `--help`, because a spec that could claim `--dry-run` could turn the
safety net off.

### Response validation

blip checks response bodies against the schema the spec declares for that status. Drift is
a warning by default, because a mismatched schema should never stop you debugging.
`--strict` turns it into a failure with exit 9, for when the contract is the thing under test.

## Specs

Point `spec_url` wherever your spec lives. With it absent, blip probes, in order:

1. `/openapi/v1.json`
2. `/swagger/v1/swagger.json`
3. `/openapi/v1.yaml`
4. `/swagger/v1/swagger.yaml`

The first two cover .NET minimal APIs, whether the spec comes from
`Microsoft.AspNetCore.OpenApi` or Swashbuckle. A docs UI such as Scalar or Swagger UI
renders a spec, it does not serve one; point blip at the JSON, not the page.

Both OpenAPI 3.0 and 3.1 are supported. .NET 10 emits 3.1, where a schema type is a
union: `"type": ["integer", "string"]` for a query parameter that may arrive
string-encoded, `["null", "string"]` for a nullable one. blip binds the flag to the
specific member, so those are an `int` flag and a `string` flag respectively.

Note that `Microsoft.AspNetCore.OpenApi` serves the spec with no `ETag` and no
`Last-Modified`, so revalidation cannot be conditional and each run refetches the
document. blip compares the bytes it gets back, so an unchanged spec is still recognised
as unchanged.

Specs are cached under `~/.cache/blip/specs/<name>/<env>/` and revalidated with
`If-None-Match` on each run, which costs one small conditional request. `--refresh` forces
a full fetch, `--offline` forbids the network and fails on a cold cache. If the backend
cannot be reached but the cache is warm, blip warns and carries on, because a backend
being down is exactly when you need this most.

A spec that requires authentication is retried with credentials, but only after an
unauthenticated attempt has been refused, so a public spec never reaches for a vault.

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

## What blip is not

Not a general HTTP client competing with curl, httpie or restish. No TUI, no REPL. No
response templating language and no `jq` reimplementation; it emits JSON and you pipe it.
No code generation, the command tree is always built from the spec at runtime. No plugins.

## Development

```sh
make lint test build     # vet, gofmt check, tests, binary
make dist                # cross-compiled binaries into dist/
make golden              # regenerate the golden files, deliberately
```

`describe --compact` and `--dry-run` output are pinned by golden files, because both are
contracts a caller parses. No test touches the network.

## License

MIT. See [LICENSE](LICENSE).
