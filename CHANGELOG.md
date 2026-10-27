# Changelog

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
