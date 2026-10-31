package torn

import (
	"encoding/json"

	"github.com/swervelord/Torn-Go-SDK/internal/contract"
	"github.com/swervelord/Torn-Go-SDK/models"
)

// decodeModel decodes raw into out with the tolerant, exact-integer rules of
// the generated models.
func decodeModel(raw []byte, out any) error { return contract.DecodeInto(raw, out) }

// decodeResponse decodes a typed response. With strict decoding (Config or
// RequestOptions) the body is first validated strictly against schema.
// Failures are *DecodeError values carrying the operation and the raw body.
func (c *Client) decodeResponse(operation, schema string, raw json.RawMessage, out any, opts RequestOptions) error {
	if schema != "" && (opts.Strict || c != nil && c.cfg.StrictDecoding) {
		if err := models.ValidateStrict(schema, raw); err != nil {
			return c.newDecodeError(operation, raw, err, true)
		}
	}
	if err := decodeModel(raw, out); err != nil {
		return c.newDecodeError(operation, raw, err, false)
	}
	return nil
}

// DecodeOperation decodes raw JSON into the generated response model of an
// operation ID and returns a pointer to it (P8.2), e.g. *GetMyAttacksResponse.
func DecodeOperation(operationID string, raw []byte) (any, error) {
	decode, ok := operationDecoders[operationID]
	if !ok {
		return nil, validationf("unknown operation %q", operationID)
	}
	v, err := decode(raw)
	if err != nil {
		return nil, (*Client)(nil).newDecodeError(operationID, raw, err, false)
	}
	return v, nil
}

func singleOptions(opts []RequestOptions) (RequestOptions, error) {
	switch len(opts) {
	case 0:
		return RequestOptions{}, nil
	case 1:
		return opts[0], nil
	}
	return RequestOptions{}, validationf("at most one RequestOptions is allowed")
}
