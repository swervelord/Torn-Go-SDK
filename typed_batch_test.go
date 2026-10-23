package torn_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	torn "github.com/swervelord/Torn-Go-SDK"
)

func TestArbitraryTypedBatchAtTransport(t *testing.T) {
	doc := loadContract(t, "6.13.8")
	payload := map[string]any{}
	for _, name := range []string{"UserProfileResponse", "UserJobResponse", "UserDiscordResponse"} {
		s := contractSample(doc.Components.Schemas[name], doc.Components.Schemas, 1, 0).(map[string]any)
		for k, v := range s {
			payload[k] = v
		}
	}
	raw, _ := json.Marshal(payload)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/user" || r.URL.Query().Get("selections") != "discord,job,profile" || r.URL.Query().Get("id") != "123" {
			t.Errorf("incorrect batch path or selections")
		}
		w.Write(raw)
	}))
	defer srv.Close()
	c, err := torn.NewClient(torn.Config{APIKeys: []string{"mock-only"}, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	batch, profile := torn.NewBatch(torn.UserSelections.Profile)
	job := torn.AddSelection(batch, torn.UserSelections.Job)
	discord := torn.AddSelection(batch, torn.UserSelections.Discord)
	if _, err := profile.Value(); err == nil {
		t.Fatal("unexecuted result accepted")
	}
	report, err := batch.Execute(context.Background(), c, map[string]any{"id": 123}, torn.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(report.Calls) != 1 {
		t.Fatalf("batch made %d HTTP requests", calls)
	}
	p, err := profile.Value()
	if err != nil || p.Profile.ID != 1 {
		t.Fatalf("profile typed decode: %v", err)
	}
	if _, err := job.Value(); err != nil {
		t.Fatal(err)
	}
	if _, err := discord.Value(); err != nil {
		t.Fatal(err)
	}
	torn.AddSelection(batch, torn.UserSelections.Profile)
	if _, err := job.Value(); err == nil {
		t.Fatal("stale batch result accepted after adding a selection")
	}
}

func TestTypedBatchCancellationWhileAnotherExecutionIsActive(t *testing.T) {
	entered := make(chan struct{}, 1)
	transport := operationTransport(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	c, err := torn.NewClient(torn.Config{APIKeys: []string{"mock-only"}, HTTPClient: &http.Client{Transport: transport}, Attempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	batch, _ := torn.NewBatch(torn.UserSelections.Profile)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { _, err := batch.Execute(ctx, c, nil, torn.RequestOptions{}); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first execution did not reach transport")
	}
	waiting, cancel := context.WithCancel(context.Background())
	cancel()
	waitDone := make(chan error, 1)
	go func() { _, err := batch.Execute(waiting, c, nil, torn.RequestOptions{}); waitDone <- err }()
	select {
	case err := <-waitDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("batch serialization ignored cancellation")
	}
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("active execution did not stop")
	}
}
