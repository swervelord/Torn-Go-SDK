package torn

const (
	// APIVersion is the pinned official contract used to generate this SDK.
	APIVersion = "6.15.0"
	// UserInventoryCacheTTLSeconds is Torn's per-category server cache lifetime.
	UserInventoryCacheTTLSeconds = 900
	// FactionInventoryCacheTTLSeconds is Torn's server cache lifetime for all armory items.
	FactionInventoryCacheTTLSeconds = 300
)

// OperationMetadata describes upstream behavior; the SDK itself does not cache responses.
// Stability retains the exact x-stability spelling from the pinned specification.
// CacheTTLSeconds is zero where this SDK has no documented cache policy.
// AccessLevel is the minimum documented key access level: Public, Minimal,
// Limited, or Full, taken from the operation's official ApiKey parameter.
type OperationMetadata struct {
	Stability       string
	CacheTTLSeconds int
	AccessLevel     string
}

// MetadataForOperation returns metadata for an official operation ID.
// The value is copied, so callers cannot change the SDK's metadata.
func MetadataForOperation(id string) (OperationMetadata, bool) {
	metadata, ok := operationMetadata[id]
	return metadata, ok
}
