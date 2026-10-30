# API coverage and evidence

The primary authority is the official [Torn OpenAPI specification](https://www.torn.com/swagger/openapi.json).
The current snapshot was fetched on October 30, 2026 with a custom user agent.
It reports version **6.15.0**. Versioned bytes, SHA-256 hashes, source URLs, and
inventories are committed in [provenance.json](spec/provenance.json).

The historical 6.13.6 contract is semantically equal to the specification in
[the API documentation repository at its pinned commit](https://github.com/swervelord/Torn-API/tree/ac4ba829bbdbdc4ef5e058b48aae5088b9d5fd22/refs/specs).
The files have different whitespace formatting but identical parsed JSON. That
repository's provenance identifies the official source URL and historical
version; the SDK retains independent generated historical artifacts.

| Public surface | Verified inventory | Evidence |
| --- | ---: | --- |
| Exact current operations | 237 | Every public exact method called through a mock HTTP transport with independently spec-derived paths, inputs, queries, response payloads, and media types |
| Current component schemas | 724 | Three independent schema-boundary samples per component, concrete decoding, validated round-trips, and rejection boundaries |
| Historical component schemas | 711 | Three historical spec-derived samples per component, independent concrete decoding and round-trips |
| Compatibility declarations | 549 | Committed neutral contracts, typed declarations, defaults/aliases/extras/custom decoding, and handwritten boundary fixtures |
| Selection capabilities | 208 | Official enums, exact endpoints, documented standalone/direct/legacy rules, current and historical selection tests |
| Maintained v1 selection catalog | 184 | Explicit resource inventories, legacy-only routing, supported/unsupported fallback tests, and evidence limitations |
| Typed convenience helpers | 210 | All JSON selection helpers, three CSV helpers, and two public aliases invoked through transport |
| Raw convenience helpers | 205 | Every non-CSV capability has a raw helper and dispatch test |
| Dedicated CSV operations | 3 | User, faction, and company exact text responses and facade dispatch |

[coverage.json](spec/coverage.json) maps the complete inventories to their public
Go API. More focused manifests describe [models and operations](spec/model-coverage.json),
[routing and helpers](spec/routing-manifest.json), and
[compatibility contracts](spec/compatibility-models.json). All are checked offline.

## Independent verification

`schema_contract_test.go` constructs samples from the pinned specification's
required/optional/nullable/enum/array/composition constraints. It does not call the
decoder to manufacture expected acceptance. There are **2,133 frozen** and
**2,172 current** acceptance samples. `concrete_models_test.go` also decodes those
samples into their actual public Go types and verifies valid round-trips.

`operations_contract_test.go` derives expected HTTP requests directly from the
specification and invokes all 237 generated public methods. `planner_test.go`
independently enumerates compatible groups and pairs from documented selections,
asserting transport request counts and encoded selection lists. It also covers
every raw helper, required paths, allowlists, CSV rejection, standalone routing,
legacy routes, and invalid target combinations. `facade_contract_test.go` invokes
all 210 typed helpers with independently supplied responses.

`typed_batch_test.go` demonstrates concrete heterogeneous result slots. A real
compiler invocation rejects mixing a faction selection into a user batch.
Runtime tests inject HTTP servers/transports and schedulers to verify retry,
quota, key rotation, route spillover, partial successful reports, merge precision,
concurrency, and cancellation without leaking permits. Fuzz targets cover model
unions, integer exactness, and recursive merge behavior.

The opt-in live profile/job and travel inventory/job tests were run successfully
during implementation and verified exactly one actual HTTP request per batch.
They read a credential only from the environment, save no private payload, and
are skipped by normal CI. `api_6150_test.go` decodes a scrubbed live travel
inventory sample offline, including a null item `uid`.

## Boundaries and intentional choices

The official current contract adds `ErrorNoCompetitionAvailable` and changes
elimination teams from `leaders` to required `captains`. Current and historical
types remain distinct, with explicit migration tests. See [MODELS.md](MODELS.md).

The official contract has four required properties without value schemas:
`ReportStockAnalysis.items[].item.stock`,
`UserCrimeDetailsBootlegging.dvd_copies`, `UserTrade.items`, and
`TornHalloweenUpgrade.type`. Their presence is
validated, and their values remain exact raw JSON. Unknown extension fields and
unconstrained log maps also preserve raw JSON. The exhaustive descriptor paths
are recorded in [typing-report.json](spec/typing-report.json).

The historical documentation does not supply a complete v1 response
specification. Compatibility types are explicit SDK contracts with documented
permissive defaults and unknown variants; they do not establish a complete
static contract for every possible legacy payload. They are kept separate from
strict official schema types. No type information is claimed where the source
does not define it. Additional ecosystem services are outside this SDK.

Counts establish API inventory coverage. They do not establish 100% code branch
coverage, every permissions combination, or every possible live game state.
Normal tests and generation need no network, credential, or other language runtime.
