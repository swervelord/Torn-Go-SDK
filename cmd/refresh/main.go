// Command refresh downloads the official specification for explicit review.
// It never replaces pinned contracts implicitly.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	out := flag.String("out", "spec/openapi-candidate.json", "destination for a reviewable candidate snapshot")
	flag.Parse()
	if err := refresh(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func refresh(out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", "https://www.torn.com/swagger/openapi.json", nil)
	if err != nil {
		return err
	}
	r.Header.Set("User-Agent", "Torn-Go-SDK-spec-refresh/1.0")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("spec HTTP status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	var doc struct{ Info struct{ Version string } }
	if err = json.Unmarshal(b, &doc); err != nil {
		return err
	}
	if doc.Info.Version == "" {
		return fmt.Errorf("missing specification version")
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	fmt.Printf("Candidate API %s saved to %s; SHA256 %x\nReview contract changes, update provenance and generator versions deliberately, then regenerate and test.\n", doc.Info.Version, out, sha256.Sum256(b))
	return nil
}
