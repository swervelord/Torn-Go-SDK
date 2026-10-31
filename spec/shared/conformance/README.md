# Shared conformance scenarios (PARITY_SPEC P9.1)

Each `*.json` file in this directory is `{"scenarios": [Scenario, ...]}`. Every SDK's test suite loads every
file, runs every scenario through its mock transport + deterministic scheduler/clock + seeded/zero jitter, and
asserts the outcome. A scenario an SDK cannot run must fail the suite, never be skipped silently.

## Scenario
```jsonc
{
  "id": "failover-incorrect-key",            // unique; used in test names
  "requirements": ["P2.4"],                  // PARITY_SPEC ids exercised
  "config": {                                // client config; omitted fields = SDK defaults from error-codes.json
    "keys": ["KEY_A", "KEY_B"],
    "maxAttempts": 4,
    "perKeyPerMinute": 100,
    "routes": ["direct"],                    // optional egress route names (each its own IP budget)
    "keyGroups": [["KEY_A","KEY_B"]],        // optional P3.2
    "checkAccess": false,
    "jitter": "none"                         // tests run without randomness: delay = base * 2^(n-1), capped
  },
  "clock": {"startMs": 0},                   // virtual clock; sleeps advance it instantly
  "preload": [ {"key": "KEY_A", "sendsAtMs": [0, 1000]} ],   // optional: requests already spent in the window
  "request": {                               // ONE logical SDK call
    "kind": "selections",                    // selections | operation | raw | paginate
    "resource": "user",
    "selections": ["profile"],
    "id": null,
    "params": {},                            // extra query params / filters, forwarded verbatim (P1.5)
    "operationId": null,                     // for kind=operation
    "path": null,                            // for kind=raw / paginate start
    "maxPages": null
  },
  "exchanges": [                             // the exact, ordered HTTP sends the SDK must perform
    {
      "expect": {
        "method": "GET",
        "path": "/v2/user",                  // path only; host must be api.torn.com
        "query": {"selections": "profile"},  // subset match; values compared as strings, list values joined by ","
        "absentQuery": ["key"],              // query names that must NOT be present
        "auth": "KEY_A",                     // v2: Authorization header must be "ApiKey KEY_A"; v1: key= query
        "atMs": 0                            // optional: virtual time of this send (>=)
      },
      "respond": {"status": 200, "headers": {}, "body": {"profile": {"id": 1, "name": "Test"}}}
    }
  ],
  "expect": {
    "outcome": "ok",                         // ok | error
    "error": {"kind": "api", "code": 2},     // when outcome=error. kinds: api|http|transport|decode|validation|no_usable_keys|cancelled
    "data": {"profile.name": "Test"},        // optional dotted-path checks on the raw merged JSON result
    "sends": 1,                              // total HTTP sends (must equal exchanges length)
    "keyStates": {"KEY_A": "disabled"},      // optional: healthy | rate_limited | disabled
    "minElapsedMs": 0,                       // optional virtual-clock assertions
    "maxElapsedMs": 0,
    "pages": 3,                              // paginate only
    "typedDecode": "ok",                     // optional: decode the result into the operation's generated model: ok|error
    "unknownEnum": "user.job.position"       // optional: path whose decoded enum must be "unknown" w/ raw preserved
  }
}
```
Notes
- After the last exchange the SDK must not send again (assert `sends`).
- `secretNotIn`: when present in `expect`, the SDK serializes the error/report/config via every public
  formatting path and asserts none contains the full key (P2.2).
