# Client execution

`NewClient(Config)` requires at least one API key. All calls accept a context. The
default v2 origin is `https://api.torn.com/v2`; the legacy origin is
`https://api.torn.com`. `ConfigFromEnv` reads `TORN_API_KEYS` (comma-separated),
falling back to `TORN_API_KEY`, plus `TORN_BASE_URL`, `TORN_TIMEOUT` (Go duration),
`TORN_ATTEMPTS` and `TORN_CONCURRENCY`. Never commit credentials.

The complete environment configuration also accepts `TORN_API_BASE_URL_V2`,
`TORN_API_BASE_URL_V1`, `TORN_HTTP_TIMEOUT_SECS`, `TORN_USER_AGENT`,
`TORN_MAX_ATTEMPTS`, `TORN_NETWORK_RETRY_BACKOFF_MS`,
`TORN_RATE_LIMIT_PER_KEY_PER_MIN`, `TORN_RATE_LIMIT_PER_IP_PER_MIN`, and
`TORN_MAX_IN_FLIGHT`. These descriptive names take precedence over the shorter
aliases above when both are present. Integer duration settings use the unit in
their name. Malformed, nonpositive, or overflowing limits are rejected. Key
whitespace is trimmed and duplicate key values share one quota identity.

## Defaults

| Setting | Default | Source |
| --- | --- | --- |
| `Timeout` (per HTTP attempt) | 30 s | SDK |
| `Attempts` (total sends) | 4 | `spec/shared/error-codes.json` |
| `RetryDelay` (backoff base) | 500 ms | `spec/shared/error-codes.json` |
| `MaxRetryDelay` | 30 s | `spec/shared/error-codes.json` |
| `RetryAfterCap` | 120 s | `spec/shared/error-codes.json` |
| `Jitter` | `JitterFull` | `spec/shared/error-codes.json` |
| `MaxConcurrency` | 8 | SDK |
| `KeyPerMinute` (sliding 60 s, per key group) | 100 | Torn |
| `RoutePerMinute` (sliding 60 s, per route) | 1,000 | SDK |
| `MaxResponseBytes` | 64 MiB | SDK |
| `CacheTTL` (only with `Cache`) | 30 s | SDK |

An outer context deadline limits the whole operation, including waiting and
retries. Request options can override attempts, timeout and route. Negative
limits are rejected; zero configuration values select defaults.

## Keys and rate limits (PARITY_SPEC P2, P3)

Each send reserves a concurrency slot, a key and a route. Keys rotate
round-robin over healthy keys. Budgets are sliding 60-second logs, so at most
`KeyPerMinute` sends fall in any rolling minute. `KeyGroups` (or
`DetectKeyOwners` with `RefreshKeyInfo`) makes keys of one Torn user share a
budget, because Torn limits per user. When no key or route has budget the
client sleeps until the earliest one frees; waits release their concurrency
permit, so a busy route never blocks callers on other routes.

Torn error codes are handled by the policy table generated from
`spec/shared/error-codes.json` (`ErrorCode.Action()`):

| Action | Codes | Behavior |
| --- | --- | --- |
| `rotate_key` | 2, 10, 13, 18 (disable for client lifetime), 14 (disable until next UTC midnight), 16 (no disable) | resend immediately with the next healthy key; does not consume an attempt; `NoUsableKeysError` when none is left |
| `wait_rate_window` | 5 | mark the key group rate limited until its oldest send leaves the window; rotate to a key with budget, else sleep |
| `rotate_route` | 8 | block the route for 60 s; resend on another ready route, else fail (never retry on the same IP) |
| `retry_backoff` | 12, 17, 24, 31 | exponential backoff `RetryDelay*2^(n-1)`, capped, full jitter |
| `reroute_version` | 22, 23 | selection calls are re-planned once on the other API version |
| `fail` | all others and unknown codes | return `*APIError` |

HTTP 429/500/502/503/504 and network or body-read failures also back off;
`Retry-After` (seconds or date) replaces the backoff, capped at
`RetryAfterCap`. Classification never inspects message text. Context
cancellation interrupts semaphore, quota, backoff and network waits.
`KeyStates()` reports each key's redacted id, status, recovery time, reason,
group, remaining budget and known access level.

`Config.CheckAccess` skips keys whose known access level (from `KeyAccess` or
`RefreshKeyInfo`) is below the operation's level from
`spec/shared/access-levels.json`, and fails before sending with `*AccessError`
when no key qualifies.

State is process-local and shared by all goroutines using one client. The
observer and hooks run synchronously; they must be concurrency safe and return
promptly (a panicking hook is recovered).

## Security

API v2 requests send the key only in the `Authorization: ApiKey <key>` header.
A key passed as a parameter (the deprecated generated `Params.Key`, a `key`
entry in a params map or `RequestOptions.Query`) is moved to the header for
that request. Legacy v1 calls use the `key` query parameter because v1 has no
header authentication. API messages, decode error excerpts and hook events
redact configured keys (`***` plus at most four characters); transport errors
keep their cause but drop request URLs. `Config`, `Client`, `Route` and
`KeyState` formatting and JSON never include keys. Redirects are returned as
HTTP errors so credentials never reach a redirect target. Bodies above
`MaxResponseBytes` fail rather than truncate.

## Reports, splitting and fallback

Execution reports include successful contributing calls only, with operation,
path, route, selections, version, fallback and cache status. A failed
multi-call execution returns its partial successful data and report alongside
the error.

Combined generic selection requests split into individual selections after
Torn code 4 or 22 or HTTP 400/404/501. A single selection rejected with code 22
is re-planned on v1, and a v1 call rejected with code 23 on v2, once. v1-only
names that the v2 selection enum lists as served by Torn's own fallback are
requested on v2 first. `RequestOptions{AllowLegacyFallback:true}` additionally
retries catalogued selections on v1 after code 4 or HTTP 400/404/501.
`DisableFallback` disables splitting and rerouting.

## Decoding, caching and hooks

Typed responses decode tolerantly (see [MODELS.md](MODELS.md)); with
`Config.StrictDecoding` or `RequestOptions.Strict` the body is first validated
strictly. Failures are `*DecodeError` values with the operation, JSON path,
expected rule, value excerpt and the raw body.

`Config.Cache` (for example `NewMemoryCache`) stores successful raw responses
for the operation's documented upstream cache lifetime or `CacheTTL`. Errors are
never cached; owner-dependent resources (user, faction, company, key) are
scoped to a fingerprint of the key set.

`Config.Hooks` receives redacted `RequestEvent` values on request, response,
retry and final error.

`Execute` recursively merges objects, retaining earlier object keys and replacing
later scalars, arrays and nulls, without passing integers through float64.
`DoOperation` and `Get` are raw escape hatches; parameters may be a struct with
`path`, `query` or `json` tags, or a string-keyed map. Slice query values use
comma-separated API encoding. Unknown parameters are forwarded verbatim.

Inject `HTTPClient`, route transports, `Scheduler` and `Observer` for
deterministic testing, or use package `torntest`. The test suite needs no key or
network access.
