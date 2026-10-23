// Command mockbatch demonstrates a complete typed batch without a key or network.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	torn "github.com/swervelord/Torn-Go-SDK"
)

type mockTransport struct{ calls int }

func (t *mockTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	if r.URL.Path != "/v2/user" || r.URL.Query().Get("selections") != "job,profile" {
		return nil, fmt.Errorf("unexpected mock batch request")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fixture)), Request: r}, nil
}

func main() {
	transport := &mockTransport{}
	client, err := torn.NewClient(torn.Config{APIKeys: []string{"mock-credential"}, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		panic(err)
	}
	result, err := torn.FetchPair(context.Background(), client, torn.UserSelections.Profile, torn.UserSelections.Job, map[string]any{"id": 123}, torn.RequestOptions{})
	if err != nil {
		panic(err)
	}
	if result.First.Profile.ID != 123 || transport.calls != 1 || len(result.Report.Calls) != 1 {
		panic("incorrect typed batch result")
	}
	fmt.Printf("%s: profile and job decoded with %d HTTP request\n", result.First.Profile.Name, transport.calls)
}

const fixture = `{
  "profile": {
    "id":123,"name":"Example","level":1,"rank":"Beginner","title":"Civilian",
    "age":1,"signed_up":1700000000,"faction_id":null,"honor_id":1,
    "property":{"id":1,"name":"Shack"},"image":null,"gender":"Male",
    "revivable":false,"role":"Civilian","status":{"description":"Okay","details":null,"state":"Okay","until":null,"color":"green"},
    "spouse":null,"awards":0,"friends":0,"enemies":0,"forum_posts":0,"karma":0,
    "last_action":{"status":"Online","timestamp":1700000000,"relative":"now"},
    "life":{"current":100,"maximum":100},"donator_status":null,"revive_setting":"Unknown"
  },
  "job":null
}`
