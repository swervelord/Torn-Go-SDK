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

The SDK defaults are a 30-second timeout **per HTTP attempt**, three attempts,
250 ms linear retry delay (250 ms before the second attempt, 500 ms before the
third), eight simultaneous sends, 100 calls per key per minute and 1,000 per
route per minute. These are configurable local policies, not guaranteed Torn
service limits. An outer context deadline limits the whole operation, including
waiting and retries. Request options can override attempts, timeout and route.
Negative limits are rejected; zero configuration values select defaults.

Each send reserves one key and route slot. Keys rotate across sends. Quotas reset
on absolute minute boundaries. Route selection follows `RouteOrder`, then any
remaining configured routes, spilling over when a route's local quota is full.
With no routes configured, the `default` route uses the default HTTP transport
(including its standard environment proxy behavior). Named routes support a
dedicated transport or proxy URL. Explicitly choosing a route waits for that
route's quota rather than spilling to another. Quotas and concurrency are shared
across callers of one client and are process-local. Other processes and clients
are not accounted for. The observer runs synchronously after quota reservation;
it must be concurrency safe and should return promptly.

Network errors, body read failures, HTTP 408/429/500/502/503/504, and Torn API codes
5/8/9 are retried within the attempt budget. HTTP `Retry-After` seconds or dates
increase the delay when longer than linear backoff. Torn code 5 waits until at
least the next minute boundary and spends the rest of that minute's local budget
for the key that received it, so concurrent and later calls rotate to other keys.
When the message names the IP as a whole word, the route's budget is spent too
and calls spill over to other routes. HTTP 429 uses Retry-After and backoff
without spending budgets. Context cancellation interrupts semaphore,
quota, backoff and network waits. Redirects are returned as HTTP errors to avoid
sending credentials to a redirect target. Response bodies default to a 64 MiB
limit (`MaxResponseBytes`); larger responses fail explicitly rather than truncate.
Quota waits release their concurrency permit so an exhausted pinned route cannot
prevent callers on other routes from sending.

Execution reports include successful contributing calls only, with operation,
path, route, selections, version and fallback status. Failed attempts do not
appear. A failed multi-call execution returns its partial successful data and
report alongside the error. API errors, HTTP status errors and transport errors
are structured. API messages redact configured keys; network errors deliberately
exclude potentially credential-bearing URL text. Client and configuration
formatting and JSON serialization omit credentials. Configured API keys remain
explicitly accessible through the configuration supplied by the caller.

Combined generic selection requests split into individual selections after Torn
code 4, HTTP 400/404/501, or a Torn message rejecting the combination ("cannot be
used together", "standalone"). Runtime v2-to-v1 recovery triggers on the same
codes or on messages directing the selection to API v1, and additionally requires
`RequestOptions{AllowLegacyFallback:true}` and explicit fallback calls in the
plan. `DisableFallback` disables both split and legacy recovery. Independent
helper invocations are independent sends; explicit batching requests coalescing.
CSV operations use their text channel (`DoCSV`) and cannot be merged as selection
batches. `DoOperation` represents CSV data as a JSON string for generic decoding.

`Execute` recursively merges objects, retaining earlier object keys and replacing
later scalars, arrays and nulls. It uses raw JSON values, preserving large integer
digits without passing through float64. `Decode[T]` uses standard `encoding/json`
and honors generated model decoding. `DecodeWith` accepts a custom typed decoder.
`DoOperation` is the raw endpoint escape hatch; its parameters may be a struct
with `path`, `query` or `json` tags, or a string-keyed map. Slice query values use
comma-separated API encoding. A missing path value fails before sending.

Inject `HTTPClient`, route transports, `Scheduler` and `Observer` for deterministic
testing. Schedulers must support concurrent callers and context cancellation.
The default scheduler uses the system clock and interruptible timers. The test
suite uses local HTTP servers and fake schedulers; ordinary tests need no key or
network access.
