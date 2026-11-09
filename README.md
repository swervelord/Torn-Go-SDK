# Torn-Go-SDK

A typed Go SDK for the official Torn API: exact endpoints, resource helpers,
explicit selection batching, current and historical models, and a resilient
request executor (key failover, sliding-window rate limits, retries with
jitter, pagination, hooks and an optional cache). Zero third-party
dependencies.

The pinned contract is Torn **6.15.7**: **237 exact operations**, **726
component schemas**, **208 selection capabilities**, **210 typed convenience
helpers**, **224 raw selection helpers** and **3 CSV operations**. An explicit
**184-selection v1 catalog** supports legacy routing; the `legacy` package keeps
**711** frozen 6.13.6 components and `compat` holds **549** lenient
compatibility declarations. The SDK implements the cross-SDK parity contract
(`spec/shared/PARITY_SPEC.md`, requirements P1-P9) shared with the Rust,
TypeScript and Zig SDKs.

## Install

```sh
go get github.com/swervelord/Torn-Go-SDK
```

Requires Go **1.23** or later (pagination uses `iter.Seq2`). Development and CI
pin Go 1.27.3 through the `toolchain` line; CI also builds and tests on Go 1.23.

## Quickstart

```go
package main

import (
    "context"
    "fmt"
    "log"

    torn "github.com/swervelord/Torn-Go-SDK"
)

func main() {
    cfg, err := torn.ConfigFromEnv() // TORN_API_KEYS or TORN_API_KEY
    if err != nil { log.Fatal(err) }
    client, err := torn.NewClient(cfg)
    if err != nil { log.Fatal(err) }

    basic, _, err := client.GetMyBasicInformation(context.Background(), torn.GetMyBasicInformationParams{})
    if err != nil { log.Fatal(err) }
    fmt.Println(basic.Profile.Name, basic.Profile.Level)
}
```

Runnable examples for every main flow are in [`example_test.go`](example_test.go)
and on pkg.go.dev.

## Authentication & keys

- v1 and v2 requests authenticate only with `Authorization: ApiKey <key>`
  (Torn v1 accepts the header). No request ever carries a key in its URL:
  the generated `Params.Key` field is deprecated and, when set, is sent in the
  header for that request (P2.1).
- `Config.APIKeys` is a pool used round-robin over **healthy** keys (P2.3).
  Codes 2, 10, 13 and 18 disable a key for the client lifetime, code 14 until
  the next UTC midnight; code 16 rotates without disabling. Failover to the
  next key is immediate and does not consume an attempt. When no key is left,
  calls fail with `*torn.NoUsableKeysError` wrapping the last API error (P2.4).
- `client.KeyStates()` returns a redacted snapshot per key: `***` plus at most
  the last four characters, status (`healthy`, `rate_limited`, `disabled`),
  recovery time, reason and remaining budget (P2.5).
- Keys never appear in `String`/`GoString`/`Format`/JSON output of `Config`,
  `Client`, errors, hook events or reports (P2.2).
- `ConfigFromEnv` reads `TORN_API_KEYS` (comma-separated) or `TORN_API_KEY`
  (P2.6) plus the optional settings documented on the function.

## Typed calls

Exact operations keep upstream operation IDs in PascalCase and take typed
parameter structs; optional parameters are pointers. Responses are concrete
`models` types. Calls without an id are scoped to the key owner (P8.1).

```go
profile, report, err := client.GetMyProfile(ctx, torn.GetMyProfileParams{})
id, _ := models.NewUserDiscordPathIdVariant1(models.UserId(123))
other, _, err := client.GetUserProfile(ctx, torn.GetUserProfileParams{ID: id})
csv, _, err := client.GetUsersSnapshot(ctx, torn.GetUsersSnapshotParams{})
job, _, err := client.UserJob(ctx, nil, torn.RequestOptions{}) // selection helper
```

Union types expose `Which()` (1-based matching variant, 0 when empty),
`WhichName()` (`"UserJob"`, `"UserCompany"`, `"null"`), `Matches(i)` and
`AsVariantN()`; constructors are `models.New<Type>VariantN`. Optional fields
use `models.Optional[T]` to distinguish missing, null and value. See
[model fidelity](MODELS.md) and [compatibility contracts](COMPATIBILITY.md).

Torn 6.15.7 additions: `UserGym` / `GetMyGym` return `Gym.UnlockedGyms`
(`[]models.UserUnlockedGym` with `ID`, `Name`, `Available`; public key, batches
with other user selections). `TornCalendarActivity` carries `EarliestStart`,
`LatestEnd` and a required `FixedStartTime`. `GetRacingRaces` and the racing
`races` selection accept `cat=available` (`models.RacingRacesCategoryEnumAvailable`:
races not yet started; `from`/`to` are ignored and `Schedule.End` is null).
Attack log participants are the `models.AttackLogAttacker`,
`AttackLogDefender` and `AttackLogItem` components behind nullable unions
(`AsVariant2` is a stealthed attack), and stock analysis reports add a nullable
`RestockTimestamp`. Array path parameters stay comma-joined
(`/torn/1,2/items`).

## Batching

```go
batch, profile := torn.NewBatch(torn.UserSelections.Profile)
job := torn.AddSelection(batch, torn.UserSelections.Job)
report, err := batch.Execute(ctx, client, map[string]any{"id": 123}, torn.RequestOptions{})
p, err := profile.Value() // models.UserProfileResponse
j, err := job.Value()     // models.UserJobResponse
```

Compatible selections become one `/user?selections=profile,job&id=123` request;
adding a faction token to a user batch is a compile error. `FetchPair` decodes
each selection independently (`FirstErr`, `SecondErr`), so one decode failure
never discards the other (P1.4). The planner splits standalone, direct-only and
incompatible selections and routes v1-only selections; see
[routing rules](ROUTING.md).

## Pagination

Every operation whose response carries `_metadata.links` has a generated
`<Op>Pager` and, when it has one list, an `<Op>Items` iterator (P7.1):

```go
for attack, err := range client.GetMyAttacksItems(ctx, torn.GetMyAttacksParams{}, torn.PageOptions{MaxItems: 500}) {
    if err != nil { return err }
    fmt.Println(attack.ID)
}

pager := client.GetMyAttacksPager(torn.GetMyAttacksParams{}, torn.PageOptions{MaxPages: 3})
page, ok, err := pager.Next(ctx) // also pager.Prev(ctx) and pager.All(ctx)
```

`client.Pages(path, params, opts)` pages raw JSON, `Paginate[T]` works for any
operation and `RawItems` iterates a raw page's list. Links are validated
against the configured API host, keys in link URLs are dropped and every page
goes through the executor (rate limits, retries, cancellation). For
`GetMyLogsPager`, `Prev` follows Torn's `prev` link, which carries the
`_metadata.nanostamp` cursor, so older logs page without gaps or repeats.

## Errors

| Type | When |
| --- | --- |
| `*torn.APIError` | Torn error envelope: `Code`, redacted `Message`, `KeyHint`, `Operation` |
| `*torn.HTTPError` | non-2xx status (`StatusCode`, `RetryAfter`) |
| `*torn.TransportError` | network/TLS/timeout; `Unwrap` gives the cause, so `errors.Is(err, context.DeadlineExceeded)` works |
| `*torn.DecodeError` | response does not fit its model: `Operation`, `Path`, `Expected`, `Value`, `Raw()` |
| `*torn.ValidationError`, `*torn.AccessError` | rejected before sending; both match `errors.Is(err, torn.ErrValidation)` |
| `*torn.NoUsableKeysError` | every key disabled or rejected; wraps the last error |
| `context.Canceled` / `DeadlineExceeded` | caller cancellation |

`torn.ErrorCode` constants (`torn.CodeBackendError`, ...) are generated from
`spec/shared/error-codes.json` with `Name`, `Message`, `Category`, `Action` and
`IsKnown`. Helpers: `torn.Code(err)`, `IsRetryable`, `IsKeyError`,
`IsRateLimit`, `IsAccessError`, `IsTransient`, `IsDecodeError` (P5).

## Rate limits & retries

- Sliding 60-second window per key (default 100 requests), not calendar
  minutes (P3.1). Keys of the same Torn user can share one budget through
  `Config.KeyGroups`, or automatically with `DetectKeyOwners` after
  `client.RefreshKeyInfo(ctx)` (P3.2).
- Code 5 marks the key (group) rate limited until its window frees and rotates
  immediately to a key with budget; the client sleeps only when none has
  budget (P3.3). Key rotations (failover and code 5) do not consume
  `Attempts`; each key gets one recovery per request for a given error (P2.4a). Routes (`Config.Routes`, one per egress IP) have their own
  budgets; code 8 blocks the route and rotates to another, never retrying on the
  same route (P3.4). State is shared by every goroutine using a client (P3.5).
- Retries are classified by error code and HTTP status, never by message text
  (P4.1). Transient codes (12, 17, 24, 31), HTTP 429/500/502/503/504 and
  network errors back off `RetryDelay * 2^(n-1)` (default 500 ms, capped by
  `MaxRetryDelay`, 30 s) with full jitter (`Jitter: torn.JitterNone` for
  deterministic tests); `Retry-After` is honored up to `RetryAfterCap` (120 s).
  `Attempts` (default 4) counts total sends (P4.2).
- Codes 22/23 re-plan a selection call once on the other API version (P4.3).
- Code 4 on a combined selection request splits it once into per-selection
  requests (caller order); each selection reports its own error (P4.5).
- Context cancellation aborts waits, backoff and in-flight requests (P4.4).

## Access levels

`MetadataForOperation(id).AccessLevel`, `OperationAccessLevel` and
`SelectionAccessLevel` report the minimum key level (`public`, `minimal`,
`limited`, `full`) from `spec/shared/access-levels.json` (P6.1). With
`Config.CheckAccess`, keys whose level is known (`Config.KeyAccess` or
`client.RefreshKeyInfo`) and insufficient are skipped, and a request no key can
serve fails before sending with `*torn.AccessError` naming the required level
(P6.2).

## Forward compatibility

Decoding is tolerant by default so additive upstream changes never break
callers (P1):

- Enums are open: unknown values decode, round-trip byte for byte and report
  `IsKnown() == false`; `<Enum>Values()` lists known values.
- Undocumented properties are kept in `AdditionalProperties`.
- `spec/shared/spec-overrides.json` relaxes required properties that Torn
  omits live (`TornHalloweenUpgrade.type`, `UserTrade.items`,
  `UserPropertyDetailsExtendedWithRent.status`).
- Unknown selections are sent as v2 generic selections and unknown query
  parameters (and `comment`) are forwarded verbatim, on v1 and v2.
- Strict validation is opt-in: `Config.StrictDecoding`,
  `RequestOptions{Strict: true}` or `models.ValidateStrict` report unknown enum
  values, undocumented properties, constraint violations and override-relaxed
  fields. Integers stay exact (`int64`, `1e3` and `1.0` accepted, overflow rejected).

## Caching

Off by default. Set `Config.Cache` to `torn.NewMemoryCache(maxEntries, nil)`
or any `torn.Cache` implementation. Only successful responses are cached; the
TTL comes from the operation's documented upstream cache (`CacheTTLSeconds`,
e.g. 900 s for user inventory) or `Config.CacheTTL` (default 30 s). Entries of
owner-dependent resources are scoped to the key set. `RequestOptions.NoCache`
bypasses the cache (P8.4).

## Hooks

`Config.Hooks` has `OnRequest`, `OnResponse`, `OnRetry` and `OnError`, each
receiving a redacted `torn.RequestEvent` (operation, URL without key, attempt,
route, key hint, status, Torn code, duration, retry delay, error) (P8.3).
`Config.Observer` still reports every reserved send.

## Testing

Package `torntest` is a consumer test kit (P8.5): a virtual `Clock`
(`torn.Scheduler`), a scripted `Transport` that records requests
(`JSON`, `TornError`, `NetworkError`, `AssertSent`) and `LoadFixture`.

The repository gate is offline and needs no credential:

```sh
go run ./cmd/check   # gofmt, snapshot provenance, generator drift (-check)
go vet ./...
go test ./...        # includes the 32 shared conformance scenarios (P9.1)
go test -race ./...  # runners with a C compiler
go build ./...
```

`go test` runs every scenario in `spec/shared/conformance/*.json` through the
mock transport and virtual clock. Live tests need `TORN_API_KEY` (or the legacy
`TORN_LIVE_TEST_API_KEY`); `TestLiveSweep` calls every operation reachable with
the key owner's ids and decodes it in tolerant mode, printing JSON paths of any
failure (P9.2):

```sh
go test -run '^TestLive' -count=1 -v -timeout 20m
```

Regenerate after changing generator inputs: `go run ./cmd/generate`,
`./cmd/generate-compat`, `./cmd/generate-routing`, `./cmd/audit`.

## Raw access

`client.DoOperation(ctx, torn.Operation{Path: "/user/attacks"}, params, opts)`
returns raw JSON for any path; `client.Get(ctx, "/torn/items?cat=Melee", nil,
opts)` accepts a query string; `client.RawSelections` and the `Raw<Resource><Selection>`
helpers return merged raw JSON. Decode into any model with `torn.Decode[T]`,
`torn.Call[T]`, `torn.DecodeOperation(id, raw)` or `models.DecodeSchema`
(P8.2). The 19 v1-only selections without a published schema
(e.g. `RawTornRackets`, `RawFactionMainnews`) stay raw; see
[COVERAGE.md](COVERAGE.md).

## Helpers

Package `tornutil` builds links (`ProfileURL`, `AttackURL`, `FactionURL`,
`CompanyURL`, `BazaarURL`, `ItemMarketURL`, `TradeURL`, `ForumThreadURL`,
`PropertyURL`, `SearchURL`) and converts Torn timestamps
(`FromTimestamp`, `ToTimestamp`, `TCT`, `NowTCT`, `FormatTCT`, `Until`) (P8.6).

## Versioning

`torn.SDKVersion`, `torn.SpecVersion` (`APIVersion`) and `torn.SpecSHA256`
identify the build; capabilities and schemas are embedded, so no files are read
at runtime (P8.7). The module follows semantic versioning; while it is `v0`,
minor releases may change the API and every break is listed in the
[changelog](CHANGELOG.md) with a migration note. Spec bumps that only add
operations, schemas or enum values are minor releases.

Coverage means the audited official v2 contract plus the maintained historical
and compatibility surfaces; Torn leaves some fields unspecified and publishes
no complete v1 schema, so those stay exact raw JSON (see
[COVERAGE.md](COVERAGE.md) and [spec/typing-report.json](spec/typing-report.json)).

MIT licensed. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
