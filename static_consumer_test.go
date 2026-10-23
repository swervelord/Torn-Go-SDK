package torn_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCrossResourceBatchRejectedByCompiler(t *testing.T) {
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-o", filepath.Join(t.TempDir(), "consumer"), "./testdata/invalid-resource")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("cross-resource batch unexpectedly compiled")
	}
	if !strings.Contains(string(out), "FactionResource") || !strings.Contains(string(out), "UserResource") {
		t.Fatalf("build failed for an unrelated reason: %s", out)
	}
}
