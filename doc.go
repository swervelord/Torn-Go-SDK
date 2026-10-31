// Package torn is a typed client for the official Torn API (v2, with v1
// legacy routing).
//
// Exact operations (GetMyProfile, GetUserProfile, ...) take typed parameter
// structs and return concrete models; selection tokens (UserSelections,
// FactionSelections, ...) batch several selections of one resource into one
// request; paginated operations have generated Pager and Items iterators.
//
// The executor rotates a key pool with immediate failover, enforces sliding
// 60-second rate windows per key group and route, retries transient failures
// with exponential backoff and full jitter, and never puts keys in v2 URLs.
// Decoding is tolerant by default: unknown enum values, undocumented members
// and spec defects listed in spec/shared/spec-overrides.json never break a
// response; strict validation is opt-in. Behavior follows the cross-SDK
// parity contract in spec/shared/PARITY_SPEC.md.
//
// The models package describes the current API. The legacy package keeps the
// frozen historical schema independent of current decoding, compat holds
// lenient compatibility bundles, torntest is a consumer test kit and tornutil
// holds Torn link and time helpers.
package torn
