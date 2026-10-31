package torn

import (
	"context"
	"strings"

	"github.com/swervelord/Torn-Go-SDK/models"
)

// AccessLevel is a Torn key access level (PARITY_SPEC P6.1). Values match
// spec/shared/access-levels.json.
type AccessLevel string

// Access levels in increasing order. Custom keys have per-selection grants and
// are never rejected client-side.
const (
	AccessPublic  AccessLevel = "public"
	AccessMinimal AccessLevel = "minimal"
	AccessLimited AccessLevel = "limited"
	AccessFull    AccessLevel = "full"
	AccessCustom  AccessLevel = "custom"
)

var accessRank = map[AccessLevel]int{AccessPublic: 1, AccessMinimal: 2, AccessLimited: 3, AccessFull: 4}

// IsKnown reports whether the level is one of the documented levels.
func (a AccessLevel) IsKnown() bool { return accessRank[a] > 0 || a == AccessCustom }

// Satisfies reports whether a key with level a may call an operation that
// requires level required. Unknown and custom levels are assumed sufficient.
func (a AccessLevel) Satisfies(required AccessLevel) bool {
	have, need := accessRank[a], accessRank[required]
	if have == 0 || need == 0 {
		return true
	}
	return have >= need
}

// AccessLevelFromKeyType converts the key/info access type ("Public Only",
// "Minimal Access", "Limited Access", "Full Access", "Custom").
func AccessLevelFromKeyType(t string) AccessLevel {
	switch strings.ToLower(strings.Fields(t + " x")[0]) {
	case "public":
		return AccessPublic
	case "minimal":
		return AccessMinimal
	case "limited":
		return AccessLimited
	case "full":
		return AccessFull
	case "custom":
		return AccessCustom
	}
	return ""
}

// OperationAccessLevel returns the minimum key access level documented for an
// operation ID.
func OperationAccessLevel(operationID string) (AccessLevel, bool) {
	m, ok := operationMetadata[operationID]
	return m.AccessLevel, ok
}

// SelectionAccessLevel returns the access level required by a selection: the
// highest level among its exact endpoints. ok is false for selections without
// a v2 endpoint (v1-only or unknown).
func SelectionAccessLevel(resource, selection string) (AccessLevel, bool) {
	resource, selection = strings.ToLower(strings.TrimSpace(resource)), strings.ToLower(strings.TrimSpace(selection))
	for _, c := range selectionCatalog {
		if c.Resource != resource || c.Name != selection {
			continue
		}
		var level AccessLevel
		for _, ep := range c.Endpoints {
			if m, ok := operationMetadata[ep.ID]; ok && accessRank[m.AccessLevel] > accessRank[level] {
				level = m.AccessLevel
			}
		}
		return level, level != ""
	}
	return "", false
}

// KeyInfo is the key/info result for one configured key (P6.2).
type KeyInfo struct {
	// Key is the redacted key ("***" plus at most four characters).
	Key    string
	Index  int
	Access AccessLevel
	// OwnerID is the Torn user ID that owns the key.
	OwnerID int64
	Info    models.KeyInfoResponse
	Err     error
}

// RefreshKeyInfo calls GET /key/info once per configured key, records each key's
// access level for CheckAccess and, with Config.DetectKeyOwners, merges keys of
// the same Torn user into one shared rate-limit budget (P3.2, P6.2).
func (c *Client) RefreshKeyInfo(ctx context.Context) ([]KeyInfo, error) {
	keys := c.pool.allKeys()
	out := make([]KeyInfo, len(keys))
	var firstErr error
	for i, key := range keys {
		out[i] = KeyInfo{Key: redactKey(key), Index: i}
		raw, _, err := c.doOperation(ctx, Operation{ID: "getKeyInfo", Path: "/key/info", Method: "GET"}, map[string]any{"key": key}, RequestOptions{NoCache: true}, "v2", nil, false, requestMeta{})
		if err == nil {
			err = c.decodeResponse("getKeyInfo", "KeyInfoResponse", raw, &out[i].Info, RequestOptions{})
		}
		if err != nil {
			out[i].Err = err
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out[i].Access = AccessLevelFromKeyType(string(out[i].Info.Info.Access.Type))
		out[i].OwnerID = int64(out[i].Info.Info.User.ID)
		c.mu.Lock()
		c.pool.keys[i].access = out[i].Access
		c.pool.keys[i].owner = out[i].OwnerID
		c.mu.Unlock()
	}
	if c.cfg.DetectKeyOwners {
		c.mu.Lock()
		c.regroupByOwner()
		c.mu.Unlock()
	}
	return out, firstErr
}

// regroupByOwner merges the rate-limit groups of keys with the same owner.
func (c *Client) regroupByOwner() {
	p := &c.pool
	byOwner := map[int64]int{}
	for i := range p.keys {
		k := &p.keys[i]
		if k.owner == 0 {
			continue
		}
		g, ok := byOwner[k.owner]
		if !ok {
			byOwner[k.owner] = k.group
			continue
		}
		if g != k.group {
			old := k.group
			for _, t := range p.groups[old].sends {
				p.groups[g].record(t)
			}
			p.groups[old].sends = nil
			for j := range p.keys {
				if p.keys[j].group == old {
					p.keys[j].group = g
				}
			}
		}
	}
}
