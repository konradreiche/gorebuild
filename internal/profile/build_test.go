package profile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCaptureRetainsMalformedGraph(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	work := t.TempDir()
	tool := filepath.Join(work, "go-fixture")
	body := "#!/bin/sh\nfor arg do\ncase \"$arg\" in -debug-actiongraph=*) graph=${arg#*=};; esac\ndone\nprintf '{broken' >\"$graph\"\nprintf 'fixture diagnostic'\n"
	if err := os.WriteFile(tool, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	capture := CaptureBuild(context.Background(), BuildOptions{Directory: work, GoBinary: tool, Target: ".", CacheDir: filepath.Join(work, "cache"), OutputPath: filepath.Join(work, "out")})
	if !strings.Contains(capture.Failure, "invalid action graph") || capture.MalformedActionGraph != "{broken" || len(capture.ActionGraph) != 0 || capture.Log != "fixture diagnostic" {
		t.Fatalf("malformed evidence lost: %+v", capture)
	}
	if _, err := json.Marshal(capture); err != nil {
		t.Fatal("malformed graph broke artifact encoding", err)
	}
}
