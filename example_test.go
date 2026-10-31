package torn_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	torn "github.com/swervelord/Torn-Go-SDK"
	"github.com/swervelord/Torn-Go-SDK/torntest"
	"github.com/swervelord/Torn-Go-SDK/tornutil"
)

// exampleClient answers from scripted responses instead of the network.
func exampleClient(responses ...torntest.Response) *torn.Client {
	c, err := torn.NewClient(torn.Config{
		APIKeys:    []string{"example-key-0001"},
		HTTPClient: &http.Client{Transport: torntest.NewTransport(responses...)},
		Scheduler:  torntest.NewClock(time.Unix(1_700_000_000, 0)),
		Jitter:     torn.JitterNone,
	})
	if err != nil {
		panic(err)
	}
	return c
}

const exampleProfile = `{"profile":{"id":1,"name":"Chedburn","level":15,"gender":"Male","status":{"description":"Okay","details":null,"state":"Okay","color":"green","until":null}}}`

// Construct a client from TORN_API_KEYS / TORN_API_KEY.
func ExampleConfigFromEnv() {
	cfg, err := torn.ConfigFromEnv()
	if err != nil {
		panic(err)
	}
	cfg.APIKeys = append(cfg.APIKeys, "fallback-key-0001")
	client, err := torn.NewClient(cfg)
	fmt.Println(client != nil, err)
	// Output: true <nil>
}

// Call an exact operation with typed parameters and a typed response. Calls
// without an id are scoped to the key owner.
func ExampleClient_GetMyBasicInformation() {
	client := exampleClient(torntest.JSON(200, exampleProfile))
	basic, _, err := client.GetMyBasicInformation(context.Background(), torn.GetMyBasicInformationParams{})
	if err != nil {
		panic(err)
	}
	fmt.Println(basic.Profile.Name, basic.Profile.Gender, basic.Profile.Gender.IsKnown())
	// Output: Chedburn Male true
}

// Batch typed selections of one resource into one request.
func ExampleNewBatch() {
	client := exampleClient(torntest.JSON(200, `{"profile":{"id":1,"name":"Chedburn","level":15,"gender":"Male","status":{"description":"Okay","details":null,"state":"Okay","color":"green","until":null}},"timestamp":1700000000}`))
	batch, basic := torn.NewBatch(torn.UserSelections.Basic)
	stamp := torn.AddSelection(batch, torn.UserSelections.Timestamp)
	report, err := batch.Execute(context.Background(), client, nil, torn.RequestOptions{})
	if err != nil {
		panic(err)
	}
	b, _ := basic.Value()
	ts, _ := stamp.Value()
	fmt.Println(len(report.Calls), b.Profile.Name, ts.Timestamp)
	// Output: 1 Chedburn 1700000000
}

// Iterate paginated items; the pager follows _metadata.links.
func ExampleClient_GetMyFactionNewsItems() {
	client := exampleClient(
		torntest.JSON(200, `{"news":[{"id":"a","text":"first","timestamp":1}],"_metadata":{"links":{"next":"https://api.torn.com/v2/faction/news?to=1","prev":null}}}`),
		torntest.JSON(200, `{"news":[{"id":"b","text":"second","timestamp":0}],"_metadata":{"links":{"next":null,"prev":null}}}`),
	)
	for item, err := range client.GetMyFactionNewsItems(context.Background(), torn.GetMyFactionNewsParams{}) {
		if err != nil {
			panic(err)
		}
		fmt.Println(item.ID, item.Text)
	}
	// Output:
	// a first
	// b second
}

// Classify failures by Torn error code, never by message text.
func ExampleIsRetryable() {
	client := exampleClient(torntest.TornError(6, "Incorrect ID"))
	_, _, err := client.GetForumTimestamp(context.Background(), torn.GetForumTimestampParams{})
	code, _ := torn.Code(err)
	var api *torn.APIError
	fmt.Println(errors.As(err, &api), code, torn.IsRetryable(err), api.KeyHint)
	// Output: true incorrect_id(6) false ***0001
}

// Decode failures carry the JSON path, the offending value and the raw body.
func ExampleDecodeError() {
	client := exampleClient(torntest.JSON(200, `{"timestamp":"soon"}`))
	_, _, err := client.GetForumTimestamp(context.Background(), torn.GetForumTimestampParams{})
	var d *torn.DecodeError
	if errors.As(err, &d) {
		fmt.Println(d.Path, d.Expected, d.Value, len(d.Raw()) > 0)
	}
	// Output: $.timestamp integer "soon" true
}

// Request any path; unknown selections and parameters pass through.
func ExampleClient_RawSelections() {
	client := exampleClient(torntest.JSON(200, `{"futureselection":{"x":1}}`))
	result, err := client.RawSelections(context.Background(), "user", []string{"futureselection"}, map[string]any{"comment": "bot"}, torn.RequestOptions{})
	fmt.Println(string(result.Data), err)
	// Output: {"futureselection":{"x":1}} <nil>
}

// Inspect key health without exposing keys.
func ExampleClient_KeyStates() {
	client := exampleClient(torntest.TornError(2, "Incorrect key"))
	_, _, err := client.DoOperation(context.Background(), torn.Operation{Path: "/user/basic"}, nil, torn.RequestOptions{})
	state := client.KeyStates()[0]
	fmt.Println(torn.IsKeyError(err), state.Key, state.Status, state.Reason)
	// Output: true ***0001 disabled incorrect_key(2)
}

// Build Torn links and convert timestamps.
func Example_helpers() {
	fmt.Println(tornutil.ProfileURL(4))
	fmt.Println(tornutil.FromTimestamp(1700000000).Format(time.RFC3339))
	// Output:
	// https://www.torn.com/profiles.php?XID=4
	// 2023-11-14T22:13:20Z
}
