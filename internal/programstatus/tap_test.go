package programstatus

import (
	"testing"

	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

func tapReport(pairs string) string { return "\x1b]7501;" + pairs + "\x1b\\" }

// TestTapObserveEventRecordsAndReports pins the shared capture contract: the
// Observer sees decoded reports, a report split across two events is
// reassembled, and a fresh tap is usable without any live stream.
func TestTapObserveEventRecordsAndReports(t *testing.T) {
	tap := NewTap(nil)

	first := tmux.StreamEvent{Target: "demo:0", Lines: []string{"\x1b]7501;state=work"}}
	if reports := tap.ObserveEvent(first); len(reports) != 0 {
		t.Fatalf("incomplete report decoded early: %+v", reports)
	}
	if _, ok := tap.Observer().Latest("demo:0"); ok {
		t.Fatal("incomplete report stored")
	}

	second := tmux.StreamEvent{Target: "demo:0", Lines: []string{"ing:app=pi" + "\x1b\\"}}
	reports := tap.ObserveEvent(second)
	if len(reports) != 1 || reports[0].State != StateWorking || reports[0].App != "pi" {
		t.Fatalf("split report not reassembled: %+v", reports)
	}
	event, ok := tap.Observer().Latest("demo:0")
	if !ok || event.State != StateWorking {
		t.Fatalf("report not recorded: %+v ok=%v", event, ok)
	}
}

// TestTapIgnoresPollingAndEmptyEvents pins the no-scrape contract: rendered
// polling captures and empty events never reach the parser.
func TestTapIgnoresPollingAndEmptyEvents(t *testing.T) {
	tap := NewTap(nil)
	for _, event := range []tmux.StreamEvent{
		{Target: "demo:0", Lines: []string{tapReport("state=done:app=pi")}, IsFull: true},
		{Target: "", Lines: []string{tapReport("state=done:app=pi")}},
		{Target: "demo:0"},
	} {
		if reports := tap.ObserveEvent(event); len(reports) != 0 {
			t.Fatalf("event decoded as authority: %+v -> %+v", event, reports)
		}
	}
	if _, ok := tap.Observer().Latest("demo:0"); ok {
		t.Fatal("polling or empty event stored a report")
	}
}

// TestTapForgetAndStopLifecycle pins the settle and lifetime contract: Forget
// removes a pane's report, and StopAll retires every active stream.
func TestTapForgetAndStopLifecycle(t *testing.T) {
	tap := NewTap(nil)
	tap.ObserveEvent(tmux.StreamEvent{Target: "demo:0", Lines: []string{tapReport("state=working:app=pi")}})
	if _, ok := tap.Observer().Latest("demo:0"); !ok {
		t.Fatal("fixture report was not recorded")
	}
	tap.Forget("demo:0")
	if _, ok := tap.Observer().Latest("demo:0"); ok {
		t.Fatal("Forget left the pane's report behind")
	}
	// A clear report has the same effect as Forget (pi's shutdown path).
	tap.ObserveEvent(tmux.StreamEvent{Target: "demo:1", Lines: []string{tapReport("state=working:app=pi")}})
	tap.ObserveEvent(tmux.StreamEvent{Target: "demo:1", Lines: []string{tapReport("state=clear:app=pi")}})
	if _, ok := tap.Observer().Latest("demo:1"); ok {
		t.Fatal("clear report did not remove the pane's status")
	}
	tap.StopAll()
	if active := tap.ListActive(); len(active) != 0 {
		t.Fatalf("StopAll left active taps: %v", active)
	}
	// Ensure on a bare tap never panics and reports no error for an empty pane.
	if err := tap.Ensure(""); err != nil {
		t.Fatalf("Ensure(\"\") = %v", err)
	}
}

// TestTapStatsCarryStreamerState pins that stats pass through to the streamer
// (serve composes program_status_panes onto them).
func TestTapStatsCarryStreamerState(t *testing.T) {
	tap := NewTap(nil)
	stats := tap.Stats()
	if stats == nil {
		t.Fatal("stats are nil")
	}
	if _, ok := stats["active_streams"]; !ok {
		t.Fatalf("stats lack active_streams: %v", stats)
	}
	if _, ok := stats["flush_interval_ms"]; !ok {
		t.Fatalf("stats lack flush_interval_ms: %v", stats)
	}
}
