# Changelog

## Unreleased: 0.1.0, cross-SDK parity (2026-10-31)

Recommended first tag: `v0.1.0` (no tags exist yet). Requirement ids refer to
`spec/shared/PARITY_SPEC.md`.

### Added
- Tolerant decoding by default: open enums with `IsKnown()` and
  `<Enum>Values()`; undocumented members kept in `AdditionalProperties`; strict
  validation opt-in through `models.ValidateStrict`, `Config.StrictDecoding` and
  `RequestOptions.Strict` (P1.1, P1.3).
- `spec/shared/spec-overrides.json` applied by the generator:
  `TornHalloweenUpgrade.type` and `UserTrade.items` may be absent (P1.2).
- `models.DecodeError` / `torn.DecodeError` with operation, JSON path,
  expected rule, redacted value excerpt and `Raw()` body; `FetchPair` reports
  per-selection errors (`FirstErr`, `SecondErr`) (P1.4).
- Unknown selections pass through as v2 generic selections; unknown query
  parameters and `comment` are forwarded on v1 and v2 (P1.5).
- Key pool states (`KeyStates()`), immediate failover per
  `spec/shared/error-codes.json`, `NoUsableKeysError`, `KeyGroups`,
  `DetectKeyOwners`, `RefreshKeyInfo` (P2.3-P2.5, P3.2, P6.2).
- Generated `ErrorCode` constants and policy table, `Code`, `IsRetryable`,
  `IsKeyError`, `IsRateLimit`, `IsAccessError`, `IsTransient`,
  `IsDecodeError`, `ValidationError`, `AccessError`, `ErrValidation` (P5).
- `AccessLevel` type, `OperationAccessLevel`, `SelectionAccessLevel`,
  `Config.CheckAccess`, `Config.KeyAccess` (P6).
- Generated `<Op>Pager` / `<Op>Items` for the 58 paginated operations,
  `Pager` (`Next`, `Prev`, `All`), `Paginate`, `Client.Pages`, `Items`,
  `RawItems` (P7.1).
- `Client.Get` raw path access and `DecodeOperation` (P8.2); `Config.Hooks`
  with redacted `RequestEvent` (P8.3); `Cache`, `NewMemoryCache`,
  `Config.CacheTTL`, `RequestOptions.NoCache` (P8.4); package `torntest`
  (P8.5); package `tornutil` (P8.6); `SDKVersion`, `SpecVersion`,
  `SpecSHA256` (P8.7).
- Union `Which()`, `WhichName()` and `Matches(i)`.
- `OperationMetadata.Paginated`, `ResponseSchema` and `Path`.
- Raw helpers for the 19 schema-less v1-only selections (`RawTornRackets`, ...).
- Shared conformance harness (30/30 scenarios), live sweep test, runnable
  `Example` functions, Go 1.23 CI job (P9.1, P9.2, P9.3, P9.5).

### Changed
- Sliding 60 s rate windows replace calendar-minute buckets; code 5 rotates
  to a key with budget and sleeps only when none has one (P3.1, P3.3).
- Retries follow `spec/shared/error-codes.json`: code 17 and other transient
  codes back off exponentially with full jitter (default base 500 ms, cap 30 s,
  `Retry-After` capped at 120 s); code 8 is never retried on the same route;
  code 9 fails fast; default `Attempts` is 4 total sends (P3.4, P4.2).
- Codes 22/23 re-plan the call on the other API version; v1-only names the v2
  enum lists as served by Torn's fallback (user bazaar/criminalrecord/display,
  torn competition/organisedcrimes) are requested on v2 first (P4.3).
- Classification never uses message text: combination splitting is triggered
  by code 4/22 or HTTP 400/404/501 only, and code 5 no longer spends a route
  because a message mentions "IP" (P4.1).
- Access levels come from `spec/shared/access-levels.json`; the generator
  verifies them against the OpenAPI key parameters (P6.1).
- `TransportError` keeps a sanitized `Cause` (`Unwrap`).
- Decoding no longer re-validates every nested subtree and parameter schemas
  are parsed once: `/torn/items` (1.1 MB) decodes in about 22 ms instead of 100 ms.
- `go` directive lowered from 1.27.0 to 1.23; default User-Agent is
  `Torn-Go-SDK/<SDKVersion>`.

### Deprecated
- Generated `Params.Key` fields: configure keys on the client. A key set there
  is sent in the `Authorization` header, never in the URL (P2.1).

### Breaking changes and migration
- `OperationMetadata.AccessLevel` is now `torn.AccessLevel` with lowercase
  values (`"limited"` instead of `"Limited"`). Compare with `torn.AccessLimited`.
- `models.Validate` / `legacy.Validate` and model decoding accept unknown enum
  values, undocumented properties and override-relaxed fields. Use
  `models.ValidateStrict` or `Config.StrictDecoding` to keep rejecting them.
  Duplicate JSON member names are still rejected.
- Union `NewXVariantN` fails when the value would decode as another variant;
  `AsVariantN` fails unless `Matches(N)`.
- `PlanSelections` no longer rejects unknown selection names, and planned calls
  forward parameters no endpoint documents instead of dropping them.
- The selection helper `Client.KeyInfo` is unchanged; the per-key inspection
  call is `Client.RefreshKeyInfo`.
- Retry defaults changed (see Changed); set `Config.Attempts`,
  `Config.RetryDelay` and `Config.Jitter` to restore custom behavior. HTTP 408
  is no longer retried.

## Unreleased — Torn API 6.15.0 (2026-10-30)

- Refresh the official pinned contract to 6.15.0, with regenerated models, exact
  operations, selection tokens, helpers, manifests, typing report, and provenance.
  `APIVersion` is now `6.15.0`.
- Add the typed user travel inventory endpoint and batch selection
  (`GetMyTravelInventory`, `UserTravelinventory`, `RawUserTravelinventory`,
  `UserSelections.Travelinventory`) with `UserTravelInventoryResponse`,
  `UserTravelInventory`, `UserTravelInventoryCapacity`, `UserTravelInventorySlot`,
  and `UserTravelInventoryItem`. Nullable slot `item` and item `uid` use the
  generated union accessors; `AsVariant2` identifies JSON null.
- Expose the minimum documented key access level as
  `OperationMetadata.AccessLevel` (`Public`, `Minimal`, `Limited`, or `Full`),
  derived from each operation's official `ApiKey*` parameter. Travel inventory
  requires a Limited key and is marked Unstable upstream.
- Add `JobPositionCasinoEnumDealer` and
  `FactionTerritoryWarResultEnumEndWithDirtyBomb`. The territory war result was
  added to the official contract without a patch note.
- Add an opt-in live travel inventory/job test alongside the profile/job test.
- Current inventory: 237 exact operations, 724 component schemas, 208 selection
  capabilities, 210 typed helpers, and 205 raw helpers. Frozen 6.13.6 schemas and
  549 compatibility declarations remain available.

## Unreleased — Torn API 6.14.0 (2026-10-27)

- Refresh the official pinned contract, generators, all current concrete models,
  exact operations, selection tokens, helpers, manifests, and provenance.
- Add typed user and Torn Halloween endpoints and batch selections, including
  treats, basket upgrade IDs, timestamps, requirements, costs, and descriptions.
- Add all 25 newly documented city stats fields. The current contract requires
  those fields; payloads matching only an older contract can fail current decoding.
- Expose upstream stability through `MetadataForOperation`, including the current
  `Untable` spelling on user Halloween. Most selections are now marked Stable;
  elimination, user search, and user snapshot retain upstream instability labels.
- Expose the 900-second user inventory and 300-second faction inventory server
  cache lifetimes. The SDK does not maintain a local response cache.
- Preserve `TornHalloweenUpgrade.type` as required exact raw JSON because the
  official contract omits its value schema. The typing report records this fourth
  upstream gap; no narrower type is invented.
- Spend a key's remaining minute budget after Torn code 5, and the route's budget
  when the message attributes the limit to the IP, so other calls rotate.
- Send the API key in the `Authorization: ApiKey` header for v2 requests instead
  of the URL query string. Legacy v1 fallback calls keep the `key` parameter.
- Recognize Torn combination-rejection and v1-migration messages for split
  recovery and opt-in v1 fallback, in addition to code 4 and HTTP 400/404/501.
- Current inventory: 236 exact operations, 719 component schemas, 207 selection
  capabilities, 209 typed helpers, and 204 raw helpers. Frozen 6.13.6 schemas and
  549 compatibility declarations remain available.
