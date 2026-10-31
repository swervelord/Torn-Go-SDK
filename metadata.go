package torn

const (
	// APIVersion is the pinned official contract used to generate this SDK.
	APIVersion = "6.15.0"
	// SpecVersion is the OpenAPI version of the pinned contract (P8.7).
	SpecVersion = APIVersion
	// SDKVersion is this module's release version (P8.7).
	SDKVersion = "0.1.0"
	// UserInventoryCacheTTLSeconds is Torn's per-category server cache lifetime.
	UserInventoryCacheTTLSeconds = 900
	// FactionInventoryCacheTTLSeconds is Torn's server cache lifetime for all armory items.
	FactionInventoryCacheTTLSeconds = 300
)

// OperationMetadata describes one official operation.
type OperationMetadata struct {
	// Stability retains the exact x-stability spelling from the pinned specification.
	Stability string
	// CacheTTLSeconds is the upstream cache lifetime documented for the
	// operation; zero where none is documented. It is the default TTL of the
	// optional response cache (P8.4).
	CacheTTLSeconds int
	// AccessLevel is the minimum key access level from
	// spec/shared/access-levels.json (P6.1).
	AccessLevel AccessLevel
	// Paginated is true when responses carry _metadata.links (P7.1).
	Paginated bool
	// ResponseSchema names the models type of the 200 response.
	ResponseSchema string
	// Path is the documented path template.
	Path string
}

// MetadataForOperation returns metadata for an official operation ID.
// The value is copied, so callers cannot change the SDK's metadata.
func MetadataForOperation(id string) (OperationMetadata, bool) {
	metadata, ok := operationMetadata[id]
	return metadata, ok
}
