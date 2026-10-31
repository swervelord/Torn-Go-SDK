package torntest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClockAndTransport(t *testing.T) {
	clock := NewClock(time.Unix(0, 0))
	if err := clock.Sleep(context.Background(), time.Second); err != nil || clock.Now() != time.Unix(1, 0) || len(clock.Sleeps()) != 1 {
		t.Fatal("clock")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if clock.Sleep(ctx, time.Second) == nil {
		t.Fatal("cancelled sleep")
	}
	mock := NewTransport(JSON(200, map[string]any{"a": 1}), TornError(2, "bad"), NetworkError())
	mock.Clock = clock
	client := &http.Client{Transport: mock}
	req, _ := http.NewRequest("GET", "https://api.torn.com/v2/user?selections=basic", nil)
	req.Header.Set("Authorization", "ApiKey SECRETKEY0000001")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"a":1}` {
		t.Fatal(string(body))
	}
	resp, _ = client.Do(req)
	var env map[string]map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env["error"]["code"] != float64(2) {
		t.Fatal(env)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("network error")
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("unscripted request accepted")
	}
	r := mock.Requests()[0]
	if r.Auth() != "SECRETKEY0000001" || r.At != time.Unix(1, 0) {
		t.Fatal(r.Auth())
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(verb, r), "SECRETKEY") {
			t.Fatal("request formatting leaked the key")
		}
	}
	if mock.Remaining() != 0 || len(mock.Requests()) != 4 {
		t.Fatal("bookkeeping")
	}
}

func TestLoadFixture(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/f.json"
	if err := writeFile(path, `{"x":1}`); err != nil {
		t.Fatal(err)
	}
	if b := MustFixture(t, path); string(b) != `{"x":1}` {
		t.Fatal(string(b))
	}
	_ = writeFile(path, `{`)
	if _, err := LoadFixture(path); err == nil {
		t.Fatal("invalid fixture accepted")
	}
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
