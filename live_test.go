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
