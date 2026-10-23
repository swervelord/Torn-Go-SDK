package torn_test

import (
	"encoding/json"
	"fmt"
	"github.com/swervelord/Torn-Go-SDK/legacy"
	"github.com/swervelord/Torn-Go-SDK/models"
	"testing"
)

// Exercise concrete generated decoders, in addition to the independent registry
// validator tests, to detect field type and composition/code generation errors.
func TestEveryConcreteComponentDecodesAndRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		version  string
		decode   func(string, []byte) (any, error)
		validate func(string, []byte) error
	}{{"6.13.8", models.DecodeSchema, models.Validate}, {"6.13.6", legacy.DecodeSchema, legacy.Validate}} {
		d := loadContract(t, tc.version)
		for n, s := range d.Components.Schemas {
			for variant := 0; variant < 3; variant++ {
				t.Run(fmt.Sprintf("%s/%s/%d", tc.version, n, variant), func(t *testing.T) {
					raw, _ := json.Marshal(contractSample(s, d.Components.Schemas, variant, 0))
					value, e := tc.decode(n, raw)
					if e != nil {
						t.Fatalf("decode %s: %v", raw, e)
					}
					round, e := json.Marshal(value)
					if e != nil {
						t.Fatal(e)
					}
					if e = tc.validate(n, round); e != nil {
						t.Fatalf("round-trip %s -> %s invalid: %v", raw, round, e)
					}
				})
			}
		}
	}
}
