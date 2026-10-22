package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// bv prints --version to stdout; beads_viewer_rust (bvr) prints it to
// stderr. Parsing only stdout reported 0.0.0 for bvr and silently dropped
// every version-gated capability.
func TestBVAdapterVersionCapturesBothStreams(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{"bv stdout", "#!/bin/sh\nprintf 'bv 0.31.0\\n'\n"},
		{"bvr stderr", "#!/bin/sh\nprintf 'bvr 0.4.1 (1.92.0, x86_64)\\n' >&2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "bv")
			if err := os.WriteFile(script, []byte(tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			got, err := NewBVAdapter().Version(context.Background())
			if err != nil {
				t.Fatalf("Version: %v", err)
			}
			if got.Major == 0 && got.Minor == 0 && got.Patch == 0 {
				t.Fatalf("Version = %+v, want a parsed semver", got)
			}
		})
	}
}
