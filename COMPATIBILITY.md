# Compatibility models

The `compat` package exposes **549 maintained model declarations** for resource
facades, selection bundles, nested values, historical response shapes and custom
payload decoders. These are concrete Go structs, explicit unions and meaningful
aliases. They complement the complete current `models` and frozen `legacy`
OpenAPI packages; their count is a separate surface, not extra endpoint coverage.

The authoritative local contract is `spec/compatibility-models.json`. This is
language-neutral metadata containing recursive primitive, named, optional, array
and map type descriptors, wire names, aliases, defaults, flattened fields and
custom decoding rules. `spec/compatibility-typing-report.json` maps every model
to its Go API and lists field types, dynamic JSON positions and custom decoders.
The report includes the contract's SHA-256. All generation uses Go only, offline:

```sh
go run ./cmd/generate-compat
go run ./cmd/generate-compat -check
go test ./compat
```

The metadata is a maintained compatibility contract. It does **not** establish
independent authentication of every historical Torn payload. The frozen official
OpenAPI source has its own provenance; historical evidence limitations remain
separate from compatibility acceptance tests. No test count should be read as
proof that all possible live game states have been observed.

Struct field names follow Go export conventions; JSON tags retain wire names.
`uint64`/`uint32` and signed integers preserve declared domains and reject
fractional values, negative unsigned values and overflow. Mathematically integral
decimal or exponent spellings, such as `1.0`
and `1e3`, decode exactly into integer fields, including integer leaves in
optional pointers, arrays and maps. Conversion uses decimal digits and bounded
exponent arithmetic; it never converts through float64 or allocates a number of
zeros proportional to an untrusted exponent. Unknown raw JSON keeps its original
number spelling. Custom enum/model unmarshalers retain their own validation.
Optional fields use
pointers: omitted and explicit-null optional fields normalize to nil, matching
this compatibility contract. This differs deliberately from current schema
presence wrappers, which distinguish omission, null and zero. Default vectors
and maps become non-nil empty collections. Default nested structs initialize
their collection defaults recursively. Explicit null is rejected for concrete
non-nullable fields; defaults apply only when a field is absent.

Aliases are accepted only when the canonical field is absent. Supplying two
distinct aliases or an alias together with the canonical name fails, even when
the values are equal. Redundant declarations of the same alias are deduplicated.
Flattened extras preserve unknown fields as `json.RawMessage`, retaining exact
digits and nested payloads. On marshaling, flattened extras return to their wire
object. JSON duplicate member names follow standard `encoding/json` behavior
(the last value wins); alias collisions are checked separately. Field matching
uses exact metadata keys, not the standard library's case-insensitive fallback.

Unions expose typed pointer members and typed `AsVariant` accessors. The decoder
tries documented alternatives in order, except where a documented discriminator
selects the shape: employment type, competition name, trade item type, organized
crime error fields and warfare signatures. Unknown variants retain their original
fields in an explicit unknown variant. `DynamicValue` uses signed `int64` where
possible, otherwise `json.Number`; this intentionally preserves values outside
the signed range and decimal precision without a float64 round trip. Its typed
members are `Null`, `Bool`, `Int64`, `Number`, `String`, `Array` and `Object`.
Truly unspecified values remain explicit raw JSON only at metadata-declared
positions, listed in the typing report.

Custom field decoding accepts company news as a keyed object or list (keyed by
ID, timestamp, then list index), nullable string-like storage capacity as a
string or exact numeric text, and employee positions as strings or objects with
a string `name`. Unsupported position/string-like values normalize to nil.
`CompanyProfileBundle.Profile()` exposes its profile; `UserReportDetails.Get`
and `UserPersonalStatsBucket.Get` return typed `DynamicValue` and presence.

The independently authored golden table in `compat/golden_test.go` covers more
than 59 scenarios: unsigned/signed bounds, defaults, missing and null fields,
aliases and collisions, unknown fields, custom decoders, discriminator routing,
exact numbers and public accessors. Expectations are explicit fixture values,
never outputs captured from the implementation under test. Fuzz seeds exercise
inventory decoding and marshaling. These tests assert the stated local contract;
they do not claim independent historical evidence.
