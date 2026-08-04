# blip ui: a terminal API explorer

Status: design, pending review
Date: 2026-08-04

## What it is

`blip ui` opens an interactive view of whatever API lives in the current repo. It
works out what that API is on its own, lists its endpoints the way Scalar lists
them in a browser, lets you fill in a request and send it, and hands you a
working code snippet in the language of your choice. When you quit, the working
directory is exactly as it was.

The pitch is the absence of setup. Every terminal REST client in the field asks
you to author request files and commit them. This one reads the description the
API already publishes about itself.

## Why it belongs in blip

blip already does most of the hard parts: fetching and caching a spec, turning
an OpenAPI document into a typed operation model, resolving credentials,
assembling and sending a request, refusing unsafe redirects, and redacting
secrets. About 3,000 of its 5,600 lines are that machinery, and none of it is
CLI-specific.

`ui` is a cobra subcommand in the existing binary. It imports the same
`internal/` packages as `send`. The cost is that every blip install links the
terminal UI's dependencies, roughly 2 to 4 MB on a 9.5 MB binary, accepted
deliberately so the tool stays one command with one name.

Note the tension with blip's stated purpose. blip's primary consumer is a coding
agent; humans are second. `blip ui` is humans only. It adds no machine-readable
surface and must not change any existing one.

## Non-goals

- Parsing route declarations out of source code.
- Starting and supervising more than the one project you asked to start.
- Distributed tracing.
- Swagger 2.0.
- Saving or sharing requests through the repo.
- History across sessions.
- gRPC and GraphQL.

## Discovery

Four sources, tried in order, stopping at the first that yields a document. A
repo may hold several APIs, so discovery returns a list. One API is a list of
one.

**1. An existing `.blip.toml`.** Checked first, because a written-down answer
beats a discovered one. It already names the base URL, spec location,
environments and credentials, so every rung below is skipped.

**2. A spec file in the repo.** The usual names in the usual places. Rare in
practice but free and instant.

**3. What the project is built with.** Read the manifests (`.csproj`,
`pyproject.toml`, `package.json`, `pom.xml`, `build.gradle`, `Cargo.toml`,
`go.mod`). Each recognised framework contributes three facts: where its spec
lives, its default port, and how to start it. Then read that stack's local run
profile for the real port and environment: `launchSettings.json` for .NET,
`.env` and script definitions for Node, `application.yml` or `application.
properties` for Spring. This is the rung that knows Smarticipate runs on 44397
and needs `ASPNETCORE_ENVIRONMENT=Development`, because both facts exist only in
`launchSettings.json`.

This rung produces a guess, not a document.

**4. What is currently listening.** Probe the guess. With no guess, list the
machine's listening sockets, keep those owned by a process whose working
directory is inside this repo, and probe those. Accept a local development
certificate on loopback only, which is the rule blip already enforces.

This rung, not rung 1, is the common path. `Microsoft.AspNetCore.OpenApi`,
FastAPI, NestJS and springdoc all build the document in memory at boot and serve
it over HTTP. Nothing reaches disk. A spec file in a repo usually means a
spec-first team or a CI publishing step.

Unlike blip's runtime probe, which tries four .NET-leaning paths because `init`
already wrote the answer down, `ui` has no config to consult. Its probe list is
the full set, ordered by whatever rung 3 suggested. FastAPI in `pyproject.toml`
means try `/openapi.json` first.

**Fallback.** If all four fail and rung 3 knows a start command, offer it: one
key starts the project with the right environment, its output streams into a
pane, and the endpoint list fills in the moment the spec answers.

## The screen

One layout, two panes. The endpoint list never leaves the screen. The right pane
has three states.

```
┌─ Smarticipate.API  v1 ────────────────── dev https://localhost:44397 ● ─┐
│ / users                        │ POST /api/users                       │
│                                │ Create a user account.                │
│ ▾ Identity                     │                                       │
│   POST  /login                 │ Body  application/json                │
│   POST  /register              │   email      string    required       │
│ ▾ Users                        │   password   string    required       │
│   GET   /api/users             │   role       string    admin | member │
│ ▸ POST  /api/users             │                                       │
│   GET   /api/users/{id}        │ Responses                             │
│   DEL   /api/users/{id}        │   201  UserResponse                   │
│ ▾ Projects                     │   400  ValidationProblemDetails       │
│   GET   /api/projects          │                                       │
└────────────────────────────────┴───────────────────────────────────────┘
  / search   ⏎ fill   c snippet   e environment   ? keys
```

**Read** is the launch state and the one the tool exists for: description, every
parameter with its type, the body broken into fields with enum values spelled
out, and the response shapes. Descriptions are markdown in practice, so they
render as markdown.

**Fill** turns the same pane into a form. Required fields marked, defaults
prefilled, enums as pickers rather than free text. Path, query, header and body
fields form one list in the order the request will use them.

**Result** shows status, timing, size, headers folded away, and the body
pretty-printed, syntax-highlighted and foldable, with a cursor in it.

The status bar carries what you forget: which API, which environment, and
whether you are authenticated.

## Navigation, keys and theme

`blip ui` adopts Cairn's vocabulary wholesale. A user who knows one knows the
other.

| Key | Action |
| --- | --- |
| `↑/↓` `j/k` | move within the focused list |
| `←/→` `tab` | switch pane |
| `n` `N` | hop to the next or previous tag group |
| `enter` | Read to Fill, Fill to send |
| `esc` | back one state |
| `/` | search endpoints |
| `t` | in Result, use the value under the cursor as the bearer token |
| `c` | cycle snippet language |
| `y` | yank what is under the cursor: snippet, URL, or response value |
| `e` | switch environment |
| `r` | refetch the spec |
| `,` | settings, including theme with live preview |
| `ctrl+t` | toggle light and dark |
| `?` | mode-aware help overlay |
| `q` | quit |

The theme system is Cairn's: a semantic `Palette` of named roles, a `NamedTheme`
carrying a dark and a light variant, JSON built-ins embedded in the binary, user
drop-ins from a themes directory, a drop-in replacing a built-in of the same name
in place, and a malformed file recorded as a warning rather than a crash. The UI
code references semantic roles only and never a hex literal.

The built-ins are `event-horizon` and `retro-82`, the same two themes already
maintained for JetBrains, Zed, Cairn and jotter.

**Method badges** map onto existing roles rather than inventing colours: read
operations take `Info`, creates take `Success`, updates take `Warning`, deletes
take `Danger`, and a deprecated operation renders `Muted` throughout.

**A new `error` token, separate from `danger`.** These are different things and
both appear on screen simultaneously. `danger` marks a destructive but valid
action, which is the `DELETE` badge in the endpoint list. `error` marks something
that went wrong: a 5xx response, a refused certificate, a spec that would not
parse, a validation failure in the form. Sharing one token means a `DELETE` that
returned 500 renders identically to a `DELETE` that succeeded.

retro-82 makes the case by itself. Its `primary` (`#faa968`), `warning`
(`#e97b3c`) and `danger` (`#f85525`) are all orange, so an error has nowhere to
stand. Its `error` is crimson `#ff2447`, taken from `ERROR_HINT` in the JetBrains
scheme, where this was already solved.

`error` falls back to `danger` when a theme leaves it unset, following the
existing `focusBg` precedent, so every theme file in circulation keeps loading
unchanged.

Cairn's help overlay is the model for `?`: mode-aware so it lists only keys that
act in the current state, scrollable, centred, width capped for readability.

## Auth

The cursor in the Result body is the mechanism. Call the login endpoint like any
other, put the cursor on the token in the reply, press `t`. It becomes the bearer
token for everything sent afterwards and the status bar shows it. No
configuration and no per-API knowledge, and it works for any API whose login is
part of its own description. The same field accepts a typed or pasted value, so
bringing a token from elsewhere is the same feature rather than a second one.

The token lives in memory, dies with the process, and is masked in snippets until
you explicitly reveal it.

When the repo has a `.blip.toml`, credentials resolve through blip's existing
store instead and none of this is needed.

## Snippets

curl, C#, JavaScript, Python. `c` cycles them.

They are generated from the fully resolved request, the same value that gets
sent, after URL assembly, parameter encoding and auth are applied. Not from the
spec. A snippet therefore cannot describe a request different from the one you
just watched succeed.

Secrets are masked through `internal/output.Redactor`, with a deliberate reveal
step before copying. Note the existing caveat: redaction matches literally, so a
snippet that re-encodes a secret defeats it. Snippet renderers must place the
secret verbatim or mask it themselves.

## What it remembers

Nothing across runs. Within a session, values you typed persist as you move
between endpoints so you are not retyping an id. Quitting discards them.

The single exception is the spec cache, which is blip's and already lives in
`~/.cache/blip/specs/`, so returning to a repo you visited yesterday is instant.

This removes an entire storage subsystem from the first version and is the honest
consequence of building the explorer rather than the daily driver.

## Failure

Every failure names what was tried. Not "no API found" but the full ledger: no
spec file, no `.blip.toml`, detected ASP.NET and expected `/openapi/v1.json` on
44397 with nothing listening, no other process serving from this directory.

A Swagger 2.0 document is refused explicitly. blip currently accepts a document
carrying a `swagger` key as spec-shaped and then parses it with an OpenAPI 3
loader, which yields endpoints with no usable field information. Silent
half-success is worse than refusal, and `ui` makes it visible.

A rejected certificate says it was self-signed and whether the host qualified for
the loopback exemption.

blip's mutating-request guard becomes a confirmation step. In a list navigated
with arrow keys, `DELETE /api/users/{id}` is one keystroke away at all times.

## blip invariants this must honor

- **stdout carries only the response body.** `ui` refuses to start when stdout is
  not a terminal, rather than negotiating with the invariant.
- **Credentials resolve lazily.** Launching and browsing must never reach for a
  vault. Only the first send does.
- **`readonly` is not overridable.** A readonly environment renders mutating
  operations as unavailable, not merely warned about.
- **Exit codes are a contract.** `ui` exits 0 on a clean quit and carries the
  existing codes for a failure to start.
- **A schema type is a set, not a value.** Field rendering must go through
  `schemaType`, never `Types.Is()`, or OpenAPI 3.1 union types render as unknown.
- **A path argument may not climb.** A filled path parameter containing `..` is
  refused in the form, before send.
- **A tag is not always a group.** Grouping reuses `build`'s existing tag logic,
  including discarding the assembly-name tag .NET adds.
- **Golden files are contracts.** No existing golden output changes.

## Testing

Discovery is table-driven over fixture project trees in `testdata`: given this
tree, assert the deduced spec path, port and start command. `blip-sandbox` and
`cairn-sandbox` are the real-service checks.

Snippets are golden files: one resolved request in, four snippets out,
regenerated only through `make golden`.

Parsing, request assembly and redaction are blip's existing suites, untouched.

bubbletea's update step is a plain function from message to state, so the
interesting behaviour (three-state pane, token capture, group hopping,
environment switching) is tested without drawing a terminal. Rendering gets
golden tests at two fixed widths.

No test touches the network.

## The shared theme module

The palette is not copied into blip. It is extracted into a small module that
Cairn, jotter and `blip ui` all import.

Three consumers already carry a version of it, and they have already drifted.
Cairn's retro-82 light sets `text` to `#0a2138`; jotter's sets it to `#24404f`
and puts `#0a2138` on a `line` token Cairn does not have. Cairn has a `focusBg`
jotter does not. Same theme, same name, two different files. A fourth copy makes
that worse, and this is the one case where a shared package is not a premature
abstraction: it is a value type with a JSON format, no behavior worth versioning,
and a real reason for all consumers to agree.

The module carries the union of what the three already use, so no existing theme
file has to change:

`base`, `surface`, `overlay`, `text`, `line`, `muted`, `primary`, `focus`,
`info`, `success`, `warning`, `danger`, `error`, `accent2`, `codeBg`, `focusBg`

with `line` falling back to `text`, `focusBg` to `surface`, and `error` to
`danger`. jotter's `$token` reference layer stays in jotter, since only jotter
has structural sections to resolve.

Migrating Cairn and jotter onto it is out of scope for this spec. `blip ui`
consumes the module; the other two move when it suits them.
