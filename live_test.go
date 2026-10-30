package torn_test

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	torn "github.com/swervelord/Torn-Go-SDK"
)

type liveCounter struct{ sends atomic.Int32 }

func (c *liveCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	c.sends.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

// This bounded opt-in test never records account data or the credential.
func TestLiveProfileJobOneRequest(t *testing.T) {
	key := os.Getenv("TORN_LIVE_TEST_API_KEY")
	if key == "" {
		t.Skip("TORN_LIVE_TEST_API_KEY is not set")
	}
	transport := &liveCounter{}
	c, e := torn.NewClient(torn.Config{APIKeys: []string{key}, HTTPClient: &http.Client{Transport: transport}, Attempts: 1})
	if e != nil {
		t.Fatal("live client configuration failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, e := torn.FetchPair(ctx, c, torn.UserSelections.Profile, torn.UserSelections.Job, nil, torn.RequestOptions{Attempts: 1, DisableFallback: true})
	if e != nil {
		t.Fatalf("live typed batch failed: %v", e)
	}
	if transport.sends.Load() != 1 || len(result.Report.Calls) != 1 {
		t.Fatalf("batch used %d HTTP sends and %d successful calls", transport.sends.Load(), len(result.Report.Calls))
	}
	if result.First.Profile.ID <= 0 {
		t.Fatal("live typed profile ID was not populated")
	}
	t.Log("Typed profile and job decoded; exactly one actual HTTP request.")
}

// This bounded opt-in test needs at least a Limited key and logs no inventory data.
func TestLiveTravelInventoryAndJob(t *testing.T) {
	key := os.Getenv("TORN_LIVE_TEST_API_KEY")
	if key == "" {
		t.Skip("TORN_LIVE_TEST_API_KEY is not set")
	}
	transport := &liveCounter{}
	c, e := torn.NewClient(torn.Config{APIKeys: []string{key}, HTTPClient: &http.Client{Transport: transport}, Attempts: 1})
	if e != nil {
		t.Fatal("live client configuration failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	options := torn.RequestOptions{Attempts: 1, DisableFallback: true}
	result, e := torn.FetchPair(ctx, c, torn.UserSelections.Travelinventory, torn.UserSelections.Job, nil, options)
	if e != nil {
		t.Fatalf("live typed travel inventory batch failed: %v", e)
	}
	if transport.sends.Load() != 1 || len(result.Report.Calls) != 1 {
		t.Fatalf("batch used %d HTTP sends and %d successful calls", transport.sends.Load(), len(result.Report.Calls))
	}
	if result.First.Travelinventory.Capacity.Base <= 0 {
		t.Fatal("live travel capacity was not populated")
	}
	exact, _, e := c.GetMyTravelInventory(ctx, torn.GetMyTravelInventoryParams{}, options)
	if e != nil {
		t.Fatalf("live exact travel inventory failed: %v", e)
	}
	if exact.Travelinventory.Capacity.Base != result.First.Travelinventory.Capacity.Base {
		t.Fatal("generic and exact travel capacity differ")
	}
	for _, slot := range exact.Travelinventory.Inventory {
		if _, e := slot.Item.AsVariant2(); e == nil {
			continue
		}
		if _, e := slot.Item.AsVariant1(); e != nil {
			t.Fatalf("live travel slot is neither null nor a typed item: %v", e)
		}
	}
	t.Logf("Typed travel inventory (%d slots) and job decoded via generic batch and exact endpoint.", len(exact.Travelinventory.Inventory))
}
