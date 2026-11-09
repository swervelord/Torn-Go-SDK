package torn_test

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	torn "github.com/swervelord/Torn-Go-SDK"
	"github.com/swervelord/Torn-Go-SDK/models"
)

type liveCounter struct{ sends atomic.Int32 }

func (c *liveCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	c.sends.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

// liveTestKey reads TORN_API_KEY or the legacy TORN_LIVE_TEST_API_KEY (P9.2).
func liveTestKey(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"TORN_API_KEY", "TORN_LIVE_TEST_API_KEY"} {
		if k := os.Getenv(name); k != "" {
			return k
		}
	}
	t.Skip("TORN_API_KEY (or TORN_LIVE_TEST_API_KEY) is not set")
	return ""
}

// This bounded opt-in test never records account data or the credential.
func TestLiveProfileJobOneRequest(t *testing.T) {
	key := liveTestKey(t)
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
	key := liveTestKey(t)
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

// liveAttempt paces live calls 0.7 s apart and, because other tools share the
// key's 100 requests/minute budget, runs fn once more after 61 s when Torn
// answers code 5. fn builds its own client so the retry starts with a fresh
// key state and send counter.
func liveAttempt(t *testing.T, fn func() error) error {
	t.Helper()
	time.Sleep(700 * time.Millisecond)
	err := fn()
	if code, ok := torn.Code(err); ok && code == torn.CodeTooManyRequests {
		t.Log("code 5; backing off 61 s")
		time.Sleep(61 * time.Second)
		err = fn()
	}
	return err
}

func newLiveClient(t *testing.T, key string) (*torn.Client, *liveCounter) {
	t.Helper()
	transport := &liveCounter{}
	c, e := torn.NewClient(torn.Config{APIKeys: []string{key}, HTTPClient: &http.Client{Transport: transport}, Attempts: 1})
	if e != nil {
		t.Fatal("live client configuration failed")
	}
	return c, transport
}

// Torn API 6.15.7: user/gym lists the unlocked gyms. Logs only counts.
func TestLiveGymUnlockedGyms(t *testing.T) {
	key := liveTestKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var result torn.Pair[models.UserGymResponse, models.UserJobResponse]
	var transport *liveCounter
	e := liveAttempt(t, func() (err error) {
		var c *torn.Client
		c, transport = newLiveClient(t, key)
		result, err = torn.FetchPair(ctx, c, torn.UserSelections.Gym, torn.UserSelections.Job, nil, torn.RequestOptions{Attempts: 1, DisableFallback: true})
		return err
	})
	if e != nil {
		t.Fatalf("live typed gym batch failed: %v", e)
	}
	if transport.sends.Load() != 1 || len(result.Report.Calls) != 1 {
		t.Fatalf("batch used %d HTTP sends and %d successful calls", transport.sends.Load(), len(result.Report.Calls))
	}
	gyms := result.First.Gym.UnlockedGyms
	if len(gyms) == 0 {
		t.Fatal("live unlocked_gyms is empty")
	}
	available := 0
	for _, g := range gyms {
		if g.ID <= 0 || g.Name == "" {
			t.Fatal("live unlocked gym without id or name")
		}
		if g.Available {
			available++
		}
	}
	t.Logf("Typed gym and job decoded in one request: %d unlocked gyms, %d available.", len(gyms), available)
}

// Torn API 6.15.7: torn/calendar activities carry earliest_start, latest_end
// and a required fixed_start_time. Also checks that comma-joined path ids
// (/torn/1,2/items) are accepted live.
func TestLiveTornCalendarAndMultiIDPath(t *testing.T) {
	key := liveTestKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	options := torn.RequestOptions{Attempts: 1, DisableFallback: true}
	var result torn.Pair[models.TornCalendarResponse, models.TimestampResponse]
	var transport *liveCounter
	e := liveAttempt(t, func() (err error) {
		var c *torn.Client
		c, transport = newLiveClient(t, key)
		result, err = torn.FetchPair(ctx, c, torn.TornSelections.Calendar, torn.TornSelections.Timestamp, nil, options)
		return err
	})
	if e != nil {
		t.Fatalf("live typed calendar batch failed: %v", e)
	}
	if transport.sends.Load() != 1 || len(result.Report.Calls) != 1 {
		t.Fatalf("batch used %d HTTP sends and %d successful calls", transport.sends.Load(), len(result.Report.Calls))
	}
	activities := append(append([]models.TornCalendarActivity(nil), result.First.Calendar.Competitions...), result.First.Calendar.Events...)
	if len(activities) == 0 {
		t.Fatal("live calendar is empty")
	}
	personal := 0
	for _, a := range activities {
		if a.EarliestStart <= 0 || a.LatestEnd <= 0 || a.Start <= 0 || a.End <= 0 {
			t.Fatal("live calendar activity without its time window")
		}
		if !a.FixedStartTime {
			personal++
		}
	}
	var items torn.GetTornItemsSpecificResponse
	e = liveAttempt(t, func() (err error) {
		var c *torn.Client
		c, transport = newLiveClient(t, key)
		items, _, err = c.GetTornItemsSpecific(ctx, torn.GetTornItemsSpecificParams{Ids: []models.ItemId{1, 2}}, options)
		return err
	})
	if e != nil {
		t.Fatalf("live /torn/1,2/items failed: %v", e)
	}
	// Only the result is asserted here: with a single shared key, a code 5 makes
	// the executor wait for the rate window and resend (P3.3), which is a second
	// send of the same exact request, not a batching failure.
	if len(items.Items) != 2 {
		t.Fatalf("multi-id items: %d sends, %d items", transport.sends.Load(), len(items.Items))
	}
	t.Logf("Typed calendar decoded (%d activities, %d on personal time); /torn/1,2/items returned 2 items.", len(activities), personal)
}

// Torn API 6.15.7: racing/races accepts cat=available (races not yet started).
func TestLiveRacingRacesAvailable(t *testing.T) {
	key := liveTestKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	options := torn.RequestOptions{Attempts: 1, DisableFallback: true}
	available := models.RacingRacesCategoryEnumAvailable
	var exact torn.GetRacingRacesResponse
	var transport *liveCounter
	e := liveAttempt(t, func() (err error) {
		var c *torn.Client
		c, transport = newLiveClient(t, key)
		exact, _, err = c.GetRacingRaces(ctx, torn.GetRacingRacesParams{Cat: &available}, options)
		return err
	})
	if e != nil {
		t.Fatalf("live exact racing races failed: %v", e)
	}
	if transport.sends.Load() != 1 {
		t.Fatalf("exact races used %d HTTP sends", transport.sends.Load())
	}
	open := 0
	for _, race := range exact.Races {
		if _, err := race.Schedule.End.AsVariant2(); err == nil {
			open++
		} else if _, err := race.Schedule.End.AsVariant1(); err != nil {
			t.Fatalf("live race end is neither null nor a timestamp: %v", err)
		}
	}
	var result torn.Pair[models.RacingRacesResponse, models.TimestampResponse]
	e = liveAttempt(t, func() (err error) {
		var c *torn.Client
		c, transport = newLiveClient(t, key)
		result, err = torn.FetchPair(ctx, c, torn.RacingSelections.Races, torn.RacingSelections.Timestamp, map[string]any{"cat": "available"}, options)
		return err
	})
	if e != nil {
		t.Fatalf("live typed racing batch failed: %v", e)
	}
	if transport.sends.Load() != 1 || len(result.Report.Calls) != 1 {
		t.Fatalf("batch used %d HTTP sends and %d successful calls", transport.sends.Load(), len(result.Report.Calls))
	}
	t.Logf("Available races decoded: %d exact (%d without an end time), %d via the generic batch.", len(exact.Races), open, len(result.First.Races))
}
