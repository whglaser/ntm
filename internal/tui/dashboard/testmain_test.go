package dashboard

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/Dicklesworthstone/ntm/tests/testutil"
)

var attentionFeedTestMu sync.Mutex

func lockAttentionFeedForTest(t *testing.T) {
	attentionFeedTestMu.Lock()
	t.Cleanup(attentionFeedTestMu.Unlock)
}

func TestMain(m *testing.M) {
	// Many dashboard rendering helpers assume colors are enabled.
	// The repo's default environment sets NO_COLOR=1, so force colors on
	// for this package's tests to keep expectations stable.
	_ = os.Setenv("NTM_NO_COLOR", "0")
	_ = os.Setenv("NTM_THEME", "mocha")

	// Real-tmux dashboard tests (integration tag) get a private server so
	// they can never touch the developer's default tmux sessions
	// (ntm-6c7of).
	cleanupTmux, err := testutil.IsolateTmuxTestProcess()
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolate dashboard tmux tests: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := cleanupTmux(); err != nil {
		fmt.Fprintf(os.Stderr, "clean up isolated dashboard tmux: %v\n", err)
		code = 1
	}

	os.Exit(code)
}
