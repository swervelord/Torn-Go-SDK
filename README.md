# Torn-Go-SDK

A typed Go SDK for the official Torn API, with exact endpoints, resource helpers,
explicit selection batching, current and historical models, and a configurable
request executor. Implementation, generation, auditing, and tests use Go and its
standard library. The module requires Go **1.27.0** or later and pins **1.27.3**
for development and CI.

```sh
go get github.com/swervelord/Torn-Go-SDK
```

The pinned current contract is Torn **6.15.0**: **237 exact operations**, **724
component schemas**, **208 selection capabilities**, **210 typed convenience
helpers**, **205 raw selection helpers**, and **3 dedicated CSV operations**.
`GetMyTravelInventory` / `UserTravelinventory` expose travel capacity and the
items carried abroad (or that would be taken on departure). They require a
Limited access key, and the selection combines with other user selections on
the generic endpoint. Slot `item` and item `uid` are nullable unions: `AsVariant1`
returns the typed value and `AsVariant2` succeeds only for JSON null.
`GetMyHalloween` / `UserHalloween` and `GetTornHalloween` / `TornHalloween`
expose the current treats and basket upgrade models. The official spelling is
`halloween`. Both require a Minimal access key; both support the generic selection
endpoints, so compatible same-resource batches combine them.
`UserInventoryCacheTTLSeconds` is 900 (per category) and
`FactionInventoryCacheTTLSeconds` is 300 (all armory items). These describe
Torn's server caches; the SDK does not cache responses. `MetadataForOperation`
returns the upstream stability string, documented inventory cache lifetime, and
minimum documented key access level (`Public`, `Minimal`, `Limited`, or `Full`).
The pinned spec spells user Halloween stability `Untable`; it is retained verbatim.

An explicit **184-selection v1 catalog** supports controlled legacy routing.
The `legacy` package independently preserves **711** historical **6.13.6**
components. The `compat` package contains **549** compatibility declarations,
including defaults, aliases, custom decoding, and unknown-field preservation.

## Typed batching

Set `TORN_API_KEY` or the comma-separated `TORN_API_KEYS` in your environment.
Then:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    torn "github.com/swervelord/Torn-Go-SDK"
)

func main() {
    cfg, err := torn.ConfigFromEnv()
    if err != nil { log.Fatal(err) }
    client, err := torn.NewClient(cfg)
    if err != nil { log.Fatal(err) }
    ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
    defer cancel()

    result, err := torn.FetchPair(ctx, client,
        torn.UserSelections.Profile, torn.UserSelections.Job,
        map[string]any{"id": 123}, torn.RequestOptions{})
    if err != nil { log.Fatal(err) }
    fmt.Println(result.First.Profile.Name)
    fmt.Println(len(result.Report.Calls)) // one successful generic user call
    // result.Second.Job is a typed union of UserJob, UserCompany, or null.
}
```

Compatible profile and job selections produce one
`/user?selections=job,profile&id=123` request. Separate helper calls make separate
requests. Standalone, direct-only, legacy, and incompatible target domains are
handled explicitly; see [routing rules](ROUTING.md).

For any number of selections, retain a typed result slot for each one:

```go
batch, profile := torn.NewBatch(torn.UserSelections.Profile)
job := torn.AddSelection(batch, torn.UserSelections.Job)
discord := torn.AddSelection(batch, torn.UserSelections.Discord)
report, err := batch.Execute(ctx, client, map[string]any{"id": 123}, torn.RequestOptions{})
if err != nil { return err }
p, err := profile.Value() // models.UserProfileResponse
j, err := job.Value()     // models.UserJobResponse
d, err := discord.Value() // models.UserDiscordResponse
```

Adding a faction token to that user batch fails at compile time. Dynamically
chosen selection lists use `RawSelections`; consumers can decode a known result
projection with `DecodeSelections[T]` or a custom decoder. Every selection in one
batch shares a target and filter set. Adding selections invalidates prior slots
until the batch executes again.

## Exact endpoints, helpers, and CSV

```go
profile, report, err := client.GetMyProfile(ctx, torn.GetMyProfileParams{})
job, report, err := client.UserJob(ctx, nil, torn.RequestOptions{})
csv, report, err := client.GetUsersSnapshot(ctx, torn.GetUsersSnapshotParams{})
```

Exact operation methods retain upstream operation IDs in Go PascalCase and use
typed parameter structs. Optional parameters are pointers. Union parameters
have validated `models.New<Type>VariantN` constructors. For example:

```go
id, err := models.NewUserDiscordPathIdVariant1(models.UserId(123))
if err != nil { return err }
profile, report, err := client.GetUserProfile(ctx, torn.GetUserProfileParams{ID: id})
```

Import `github.com/swervelord/Torn-Go-SDK/models` for current schemas,
`github.com/swervelord/Torn-Go-SDK/legacy` for frozen schemas, and
`github.com/swervelord/Torn-Go-SDK/compat` for compatibility facade models.
Known fields are concrete Go values. Optional fields preserve missing versus
null; union accessors return concrete alternatives. See [model fidelity and
migration](MODELS.md) and [compatibility contracts](COMPATIBILITY.md).

## Configuration and errors

Programmatic configuration supports key rotation, quota limits, routes/proxies,
transport injection, timeout, retry delay, concurrency, and deterministic timing.
Defaults are 30 seconds per HTTP attempt, three attempts, 250 ms linear retry
delay, eight in-flight sends, 100 calls/key/minute, and 1,000 calls/route/minute.
These are configurable local policies. Outer context deadlines cover waiting,
backoff, and network execution. Quotas are shared by callers of one client.

Use `errors.As` with `*torn.APIError`, `*torn.HTTPError`, or
`*torn.TransportError` and `errors.Is` for caller cancellation. Reports record
successful contributing calls, paths, versions, routes, selections, and fallback
status. Failed attempts are omitted; multi-call errors retain partial successful
data and reports. `DoOperation`, `DoCSV`, explicit `Plan` execution, `Decode[T]`,
and `DecodeWith` provide escape hatches. See [runtime configuration](RUNTIME.md).

## Generation and verification

All normal checks are offline and need no credential:

```sh
go run ./cmd/generate
go run ./cmd/generate-compat
go run ./cmd/generate-routing
go run ./cmd/audit
go run ./cmd/check
go vet ./...
go test ./...
go test -race ./... # supported native runners with a C compiler
go build ./...
go run ./examples/mockbatch
```

`cmd/check` verifies formatting, snapshot hashes/inventories, and generated
artifact freshness. `cmd/refresh` downloads an official candidate snapshot to a
new file for review; it does not silently replace pinned contracts. Update the
versioned snapshot, provenance, generator versions, and migration tests together.

Tests cover every exact operation and every convenience helper at the HTTP
transport boundary, thousands of independently specified routing/model cases,
concrete model round-trips, current/legacy migration, custom decoder fixtures,
and deterministic retry, quota, route, cancellation, and concurrency behavior.
Opt-in read-only tests verify that live typed profile/job and travel
inventory/job batches each use exactly one actual HTTP request, and that the
exact travel inventory endpoint decodes. The travel inventory test needs a
Limited key:

```sh
# Set TORN_LIVE_TEST_API_KEY in the environment first.
go test -run '^TestLive' -count=1 -v
```

CI runs formatting, generation/provenance checks, vet, tests, and builds on
Linux, Windows, and macOS, with race checks on Linux and macOS.

## Coverage boundary

Coverage means the audited official v2 contract and explicitly maintained
historical/compatibility surfaces. It does not mean every live game state or
100% code branch coverage. Torn leaves some fields unspecified, and the
documentation does not contain a complete v1 response specification. Such
values remain exact raw JSON, with the limitation recorded in the
[coverage audit](COVERAGE.md) and [machine-readable typing report](spec/typing-report.json).
Yata, TornStats, and Weav3r are separate services outside this SDK.

MIT licensed. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
