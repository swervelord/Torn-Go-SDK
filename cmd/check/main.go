// Command check verifies formatting, pinned provenance, and generated artifacts offline.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func check() error {
	if err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".cache") {
			return fs.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := format.Source(b)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !bytes.Equal(b, f) {
			return fmt.Errorf("gofmt required: %s", path)
		}
		return nil
	}); err != nil {
		return err
	}
	var p struct {
		Snapshots []struct {
			Version, File, SHA256 string
			Operations, Schemas   int
		}
	}
	b, err := os.ReadFile("spec/provenance.json")
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return err
	}
	for _, s := range p.Snapshots {
		b, err := os.ReadFile(s.File)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != s.SHA256 {
			return fmt.Errorf("snapshot hash mismatch: %s", s.File)
		}
		var doc struct {
			Info       struct{ Version string }
			Paths      map[string]map[string]json.RawMessage
			Components struct{ Schemas map[string]json.RawMessage }
		}
		if err = json.Unmarshal(b, &doc); err != nil {
			return err
		}
		n := 0
		for _, methods := range doc.Paths {
			for method := range methods {
				switch method {
				case "get", "post", "put", "patch", "delete", "options", "head":
					n++
				}
			}
		}
		if doc.Info.Version != s.Version || n != s.Operations || len(doc.Components.Schemas) != s.Schemas {
			return fmt.Errorf("inventory mismatch: %s", s.File)
		}
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	for _, args := range [][]string{{"run", "./cmd/generate", "-check"}, {"run", "./cmd/generate-compat", "-check"}, {"run", "./cmd/generate-routing", "-check"}, {"run", "./cmd/audit", "-check"}} {
		cmd := exec.Command(goBin, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	fmt.Println("Formatting, snapshot provenance, inventories, and generated artifacts verified.")
	return nil
}
