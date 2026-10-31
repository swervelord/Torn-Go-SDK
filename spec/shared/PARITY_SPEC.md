# Torn SDK parity specification

Normative contract for the Rust, TypeScript, Go and Zig Torn SDKs. Every requirement applies to **all four**
SDKs, implemented idiomatically per language but with identical *behavior*. Requirement ids (`P1.2`) are cited in
code comments, tests, and CHANGELOGs. Shared data lives in `spec/shared/` (vendored byte-identically, never edited
in an SDK repo; regenerate with the `torn-sdk-sync` skill): `error-codes.json`, `access-levels.json`,
`spec-overrides.json`, `conformance/*.json`, and this file.

## P1 Forward-compatible decoding (no outages from additive upstream changes)
- P1.1 **Open enums.** Decoding an enum value not in the SDK's spec must succeed, preserving the raw string
  (Rust: `Unknown(String)` variant with custom (de)serialize; TS: union of literals `| (string & {})` and the runtime
  validator accepts unknown strings; Go: string-backed type, validation accepts unknown, `IsKnown() bool`;
  Zig: tagged union / wrapper with `.known`/`.unknown` (raw slice owned by the result arena)). Serializing an
  unknown value round-trips it byte-exact. Every enum type exposes "is known" and the list of known values.
- P1.2 **Spec overrides.** `spec/shared/spec-overrides.json` lists spec defects proven by the live sweep (e.g. a
  `required` property that Torn omits). Generators must apply them (making the property optional/nullable as the
  override says) so typed decoding of real responses succeeds.
- P1.3 **Strictness is opt-in.** Default decode = tolerant (P1.1 + P1.2, unknown properties kept). A `strict`
  validation mode remains available (used by the SDK's own tests/CI) that reports unknown enum values, unknown
  properties, and override-relaxed fields as errors.
- P1.4 **Never lose data.** A decode failure error carries: operation/selection, JSON path, expected type/schema,
  offending value excerpt (truncated, redacted), and access to the raw response body. In multi-selection batches,
  a decode failure of one selection must not discard the others (per-selection result or error).
- P1.5 **Unknown selections pass through.** Requesting a selection name the SDK doesn't know (new upstream) must
  be sent as a v2 generic selection (`/{resource}?selections=...`) and return raw JSON (+ generic typed decode
  helper), never be refused client-side. Unknown query params/filters are forwarded verbatim, never silently
  dropped. `comment` is forwarded on v1 and v2.

## P2 Keys, auth, secrets
- P2.1 v2 auth only via `Authorization: ApiKey <key>`; v1 via `key=` query. No public API may put a key in a v2
  URL (remove/deprecate any `key` field on generated v2 params; if a caller passes one, move it to the header).
- P2.2 Keys never appear in any Debug/Display/inspect/toString/JSON/String()/format output, error message, log,
  hook payload, or report. Show `***` + last 4 chars at most. Tests assert this for every public type holding a key
  (Rust: manual `Debug` impls; TS: `toJSON` + `[Symbol.for('nodejs.util.inspect.custom')]`; Go: `String()`/
  `GoString()`/`Format`; Zig: `format`).
- P2.3 Key pool with round-robin over **healthy** keys. Key state: `healthy | rate_limited(until) |
  disabled(reason, until?)`. Policy is data-driven from `error-codes.json` (`action`, `disable_key`).
- P2.4 **Failover:** on an error whose action is `rotate_key`, the same request is retried immediately with the
  next healthy key (does not consume backoff). Codes with `disable_key != none` disable the key for the client
  lifetime (or until next UTC midnight for code 14). Code 16 rotates without disabling. When no healthy key
  remains, fail with a typed "no usable keys" error that contains the last API error.
- P2.4a Key rotations (`rotate_key`, and code 5 moving to another key) do **not** consume `max_attempts`; they
  are bounded by the pool (each key is tried at most once per logical request for a given error). `max_attempts`
  counts sends on the same key for `retry_backoff`/HTTP/network retries.
- P2.5 Public introspection: `keyStates()` / equivalent returning per-key redacted id, state, remaining budget.
- P2.6 Env loading: `TORN_API_KEY` (single) and `TORN_API_KEYS` (comma-separated) in `fromEnv`.

## P3 Rate limiting
- P3.1 **Sliding window** (rolling 60 s log), not calendar minutes. Default 100 requests / 60 s per key.
- P3.2 Optional key grouping: keys declared (or detected via `key/info` when `detectKeyOwners` is enabled) to
  belong to the same Torn user share one budget (Torn limits per user, not per key).
- P3.3 On code 5: mark that key (group) `rate_limited` until its window frees, rotate to another healthy key
  immediately if one has budget; only sleep when none does (sleep until the earliest key frees).
- P3.4 Per-route (egress/IP) budgets keep working; code 8 marks the route blocked and rotates to another route
  if configured, else fails (no retry on the same IP).
- P3.5 Thread/task-safe, shared across clones of the client.

## P4 Retries
- P4.1 Classification by **error code** from `error-codes.json` and HTTP status, never by English message text.
- P4.2 `retry_backoff` codes, HTTP `429/500/502/503/504` and network errors retry with exponential backoff
  (`base_delay_ms * 2^(attempt-1)`, capped at `max_delay_ms`) with **full jitter**; `Retry-After` honored, capped at
  `retry_after_cap_ms`. Defaults from `error-codes.json.retry`. All configurable; `max_attempts` counts total sends.
- P4.3 `reroute_version` (codes 22/23) re-plans the request on the other API version once. Routing of v1-only
  selections: names the v2 spec's `<Resource>SelectionName` description lists as server-side v1 fallbacks (e.g.
  user `bazaar`, `criminalrecord`, `display`, `networth`) are sent to v2 first; other v1-only selections go straight
  to v1. A legacy/opt-in mode may restore older routing.
- P4.5 Rejected combinations: when a multi-selection request fails with code 4 (wrong fields), split it once into
  per-selection requests (same key) and merge the results; a selection that still fails reports its own error
  (P1.4 per-selection results). Never inspect message text.
- P4.6 Env: `TORN_API_KEYS` (comma-separated) takes precedence over `TORN_API_KEY`.
- P4.4 Cancellation/deadline aborts waits and in-flight requests (Go ctx, TS AbortSignal, Rust drop + timeout,
  Zig deadline/cancel token).

## P5 Errors
- P5.1 Generated `TornErrorCode` enum from `error-codes.json` (all codes, with names, messages, category, action) +
  open "unknown code" handling.
- P5.2 Error taxonomy: `Api{code, message, keyHint, operation}`, `Http{status}`, `Transport{cause}`,
  `Decode{...P1.4}`, `Validation`, `NoUsableKeys{last}`, `Cancelled/Timeout`. Causes chained (Go `Unwrap`,
  Rust `source()`, TS `cause`, Zig error + diagnostic).
- P5.3 Helpers: `isRetryable`, `isKeyError`, `isRateLimit`, `isAccessError`, `isTransient`, `code()`.

## P6 Access levels
- P6.1 Every operation/selection exposes its required access level from `access-levels.json`
  (`public|minimal|limited|full|custom`).
- P6.2 `keyInfo()` typed call + optional `checkAccess` mode: when the key's level is known and insufficient, fail
  before sending with a typed access error naming the required level.

## P7 Pagination
- P7.1 Every operation with `_metadata.links` gets a pager: iterate pages and items following `links.next`
  (and `prev` backwards), with `maxPages`/`limit` controls, rate-limit aware, cancellable. The key is always
  re-attached via header, never taken from the URL; `links` URLs are parsed and validated against the API host.
  (Rust: `Stream` + `next_page()`; TS: `AsyncIterable` pages and items; Go: `iter.Seq2[T, error]` + explicit
  `Pager`; Zig: `Pager` with `next()`.)

## P8 Developer experience
- P8.1 Self-scoped calls work without an id (`user().profile()` == key owner) wherever Torn supports it.
- P8.2 Raw escape hatch: arbitrary path + query -> raw JSON, plus typed decode into any generated model.
- P8.3 Hooks: `onRequest`, `onResponse`, `onRetry`, `onError` (redacted payloads: operation, url without key,
  attempt, status, duration, key hint, route).
- P8.4 Optional response cache: pluggable interface + in-memory TTL implementation; default TTL per operation from
  the spec's upstream cache metadata; off by default; never caches errors.
- P8.5 Consumer test kit: mock transport with scripted responses + request assertions, and fixture loading.
- P8.6 Torn helpers module: URL builders (profile, attack, faction, company, bazaar, item market, trade, forum
  thread, property), Torn timestamp <-> native datetime, `TCT` (UTC) helpers.
- P8.7 Runtime constants: `SDK_VERSION`, `SPEC_VERSION` (OpenAPI version), spec sha256; capabilities/spec data
  embedded (no filesystem paths required at runtime).

## P9 Quality & packaging
- P9.1 Shared conformance suite: each SDK runs every scenario in `spec/shared/conformance/*.json` through its
  mock transport in its normal test command (see `conformance/README.md` for the format). Count must match.
- P9.2 Live tests gated by `TORN_API_KEY` (also accept legacy `TORN_LIVE_TEST_API_KEY`), including a **live
  sweep**: every typed operation reachable with the key's own ids is called and decoded in default (tolerant) mode;
  failures fail the test and are printed with JSON path.
- P9.3 Generators deterministic with `--check`; `.gitattributes` forces LF for generated/spec files so Windows
  checkouts pass the drift check; CI runs on Linux + Windows (+ macOS where cheap).
- P9.4 Docs: README with the same section order in all SDKs: Install, Quickstart, Authentication & keys, Typed
  calls, Batching, Pagination, Errors, Rate limits & retries, Access levels, Forward compatibility, Caching,
  Hooks, Testing, Raw access, Helpers, Versioning; CHANGELOG with requirement ids.
- P9.5 Packaging: TS ships compiled ESM `dist/*.js` + `.d.ts` that run on Node >= 20, Bun, Deno and browsers/
  workers (no runtime `Bun.*` globals; Bun-specific features behind feature detection); Rust crate publishable
  (`cargo publish --dry-run` passes, excludes historical specs); Go module documented with `Example` funcs and a
  `go` directive no newer than needed; Zig package installable from a real tag in README.
