package session

import (
	"fmt"
	"os"
	"testing"

	"github.com/Dicklesworthstone/ntm/tests/testutil"
)

// TestMain isolates every real-tmux session test behind a private tmux server
// (private TMUX_TMPDIR socket root) so the suite can never create, enumerate,
// or kill sessions on the developer's default tmux server (ntm-6c7of).
func TestMain(m *testing.M) {
	cleanupTmux, err := testutil.IsolateTmuxTestProcess()
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolate session tmux tests: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := cleanupTmux(); err != nil {
		fmt.Fprintf(os.Stderr, "clean up isolated session tmux: %v\n", err)
		code = 1
	}

	os.Exit(code)
}
