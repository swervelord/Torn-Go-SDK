# Models and wire fidelity

The `models` package represents all 712 official 6.13.8 components. The `legacy` package independently represents all 711 components in the pinned 6.13.6 snapshot. Neither package aliases the other. Generation also names nested objects, parameter unions, inline enums, and exact-operation response bodies. These additional declarations express actual schemas; they are not an artificial declaration-count target.

## Working with values

Response objects expose concrete fields, including nested structs, typed arrays, maps, and named enums. JSON snake case becomes Go PascalCase; standalone `id`, `url`, `api`, and `csv` become `ID`, `URL`, `API`, and `CSV`. Existing component names stay stable. Inline types append `Field` when their natural name conflicts with an existing component.

Optional fields use `Optional[T]`. Inspect `Present` before reading `Value`, and inspect `Null` to distinguish explicit null. Construct values with `Some(value)` and `Null[T]()`. Missing optional properties are omitted on re-encoding. Required properties use concrete fields; JSON decoding checks presence before decoding them. Required nullable properties use explicit union types, so missing and explicit null remain different at the object boundary.

Unions expose `AsVariant1()`, `AsVariant2()`, etc., in specification order. Each accessor validates that branch before returning its concrete type. `New<Type>VariantN(value)` validates the selected branch before constructing the union; a permissive sibling cannot silently accept an invalid selected enum. `RawJSON()` returns a defensive copy for inspecting null or performing a custom decode. For example, `UserJobResponse.Job.AsVariant1()` returns `UserJob`, variant 2 returns `UserCompany`, and variant 3 represents null. A payload with `"job": null` is valid; a response with no `job` property fails decoding.

Use `json.Unmarshal` into a known type for normal static use. `Validate(name, data)` validates a component or generated inline schema. `DecodeSchema(name, data)` dynamically returns a pointer to the actual component type when its name is chosen at runtime. `Schema(name)` exposes pinned contract metadata, including documented defaults. Defaults are metadata: decoding does not silently insert values for omitted properties.

## Deliberate JSON choices

The implementation uses the stable standard `encoding/json` API plus a contract-aware parser. It rejects duplicate object names, extra trailing JSON, wrong case for required property names, missing required properties, invalid enum values, incorrect primitive types, and invalid union alternatives. Declared bounds, array limits, and string patterns are checked. Known property matching is case sensitive. Unknown properties remain in `AdditionalProperties` as `json.RawMessage` and round-trip without converting numbers to floating point.

Integers use signed 64-bit values, including fields whose upstream `format` says `int32`. This deliberately permits large Torn identifiers/counters while preventing overflow and preserving values above 2^53 exactly. Values outside the signed 64-bit domain are rejected. Integral JSON decimal and exponent forms such as `1.0` and `1e3` decode exactly into integer fields, including nested objects and arrays. Normalization never allocates in proportion to an untrusted exponent. Decimal `number` fields use `json.Number`; unconstrained payloads use raw JSON. No arbitrary integer data passes through `float64` during decoding or validation. JSON Schema `format` labels such as URI/date-time are descriptive rather than additional validation rules.

Torn has overlapping `oneOf` declarations, including a closed string enum alongside unrestricted `string`. Strict exactly-one matching would reject the enum's own documented values. The SDK consequently accepts any matching declared branch for both `oneOf` and `anyOf`, while each typed accessor still validates its selected branch. This compatibility choice is intentional and does not invent an unknown branch for closed enums.

An upstream required property without a corresponding property schema remains required, but its value uses `json.RawMessage`: the source provides no narrower contract. These anomalies are retained rather than inventing fields or ignoring requiredness. Their details are included in the audit typing-gap report.

## Current and historical elimination models

`models.TornEliminationTeam` requires `captains`, with a nullable captain and at most five vice captains. `legacy.TornEliminationTeam` requires the historical `leaders` property. An old payload does not decode as a current team. The added current `ErrorNoCompetitionAvailable` type validates error code 33. Migration must explicitly choose the intended version's package and map changed fields.

## Verification

Independent tests build three documented boundary samples per current and historical component, validate all samples, decode all samples into their concrete generated types, and verify valid JSON round-trips. Further tests reject missing required fields, invalid enums, duplicate names, fractional integers, overflow, and trailing JSON. Handwritten fixtures cover required null, zero, false, exact large integers, unknown fields, case sensitivity, and historical migration. Fuzz seeds exercise the job union.

Every exact endpoint method is invoked against an injectable HTTP transport with paths, query parameters, response schemas, and media types derived independently from the official pinned specification. This verifies all 234 operations, including all three dedicated CSV operations, with actual request construction and typed response decoding.
