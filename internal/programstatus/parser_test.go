package programstatus

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

const (
	esc    = "\x1b"
	belStr = "\x07"
	st     = esc + "\\"
)

func stReport(pairs string) string  { return esc + "]7501;" + pairs + st }
func belReport(pairs string) string { return esc + "]7501;" + pairs + belStr }
func b64(s string) string           { return base64.StdEncoding.EncodeToString([]byte(s)) }

func fixedClock(t *testing.T, at time.Time) func() time.Time {
	t.Helper()
	return func() time.Time { return at }
}

func states(events []Event) []State {
	out := make([]State, 0, len(events))
	for _, event := range events {
		out = append(out, event.State)
	}
	return out
}

// TestParserDecodesWorkingToDoneTransition uses a repaint-heavy fixture
// (SGR colours, cursor moves, an unrelated title OSC, banner text) so the
// parser proves it finds reports inside a real TUI byte stream, not just in
// clean sequences.
func TestParserDecodesWorkingToDoneTransition(t *testing.T) {
	fixture := esc + "[?25l" + esc + "[2m" +
		"\x1b]0;pi\x07" +
		"──── ⠹ Working ────\r\n" +
		stReport("state=working:app=pi:msg="+b64("demo-review")) +
		esc + "[1A" + esc + "[K\r" +
		stReport("state=done:app=pi:msg="+b64("demo-review")) +
		esc + "[?25h"

	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	parser := NewParser("%7")
	parser.SetClock(fixedClock(t, at))

	events := parser.Feed([]byte(fixture))
	if got := states(events); len(got) != 2 || got[0] != StateWorking || got[1] != StateDone {
		t.Fatalf("states = %v, want [working done]", got)
	}
	for i, event := range events {
		if event.PaneID != "%7" {
			t.Errorf("event %d pane = %q, want %%7", i, event.PaneID)
		}
		if event.App != "pi" {
			t.Errorf("event %d app = %q, want pi", i, event.App)
		}
		if event.Message != "demo-review" {
			t.Errorf("event %d message = %q, want demo-review", i, event.Message)
		}
		if !event.ObservedAt.Equal(at) {
			t.Errorf("event %d observed_at = %v, want %v", i, event.ObservedAt, at)
		}
		if !event.IsReport() {
			t.Errorf("event %d should be a report", i)
		}
	}
}

func TestParserAcceptsBELTerminator(t *testing.T) {
	parser := NewParser("%1")
	events := parser.Feed([]byte(belReport("state=idle")))
	if len(events) != 1 || events[0].State != StateIdle {
		t.Fatalf("events = %+v, want one idle event", events)
	}
}

// TestParserSplitWritesAtEveryBoundary feeds a fixture one byte at a time and
// one byte split at every possible index; the combined output must be
// identical, because a pane tap may hand the parser any chunking the kernel
// chooses.
func TestParserSplitWritesAtEveryBoundary(t *testing.T) {
	fixture := []byte(
		"repaint " + esc + "[2J" +
			stReport("state=working:app=pi:kind=:msg="+b64("long step")) +
			"\r\nmore text " +
			belReport("state=blocked:app=pi:kind=trust") +
			"trailing " +
			stReport("state=clear"),
	)

	feed := func(chunks ...[]byte) []Event {
		parser := NewParser("pane")
		var events []Event
		for _, chunk := range chunks {
			events = append(events, parser.Feed(chunk)...)
		}
		return events
	}

	want := feed(fixture)
	if got := states(want); len(got) != 3 || got[0] != StateWorking || got[1] != StateBlocked || got[2] != StateClear {
		t.Fatalf("single-shot states = %v, want [working blocked clear]", got)
	}

	for split := 0; split <= len(fixture); split++ {
		got := feed(fixture[:split], fixture[split:])
		if len(got) != len(want) {
			t.Fatalf("split at %d: %d events, want %d", split, len(got), len(want))
		}
		for i := range want {
			if got[i].State != want[i].State || got[i].Kind != want[i].Kind || got[i].Message != want[i].Message {
				t.Fatalf("split at %d: event %d = %+v, want %+v", split, i, got[i], want[i])
			}
		}
	}

	// One byte at a time: the strictest possible chunking.
	oneByte := feed(splitEveryByte(fixture)...)
	if len(oneByte) != len(want) {
		t.Fatalf("byte-at-a-time: %d events, want %d", len(oneByte), len(want))
	}
}

func splitEveryByte(data []byte) [][]byte {
	chunks := make([][]byte, 0, len(data))
	for _, b := range data {
		chunks = append(chunks, []byte{b})
	}
	return chunks
}

func TestParserDecodesQueryForm(t *testing.T) {
	parser := NewParser("%2")
	events := parser.Feed([]byte(esc + "]7501;?" + st))
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one query event", events)
	}
	event := events[0]
	if !event.Query {
		t.Fatal("query flag not set")
	}
	if event.IsReport() {
		t.Fatal("query must not report a state")
	}
	if event.State != "" || event.App != "" {
		t.Fatalf("query decoded state=%q app=%q, want empty", event.State, event.App)
	}
}

func TestParserIgnoresOtherEscapeSequences(t *testing.T) {
	fixture := "\x1b]0;window title\x07" + // OSC 0 title, BEL
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\" + // OSC 8 hyperlink
		"\x1b]7500;state=working\x1b\\" + // wrong number
		"\x1b]75010;state=working\x1b\\" + // 7501 with trailing digit
		"\x1b[31mred\x1b[0m" +
		stReport("state=error:app=pi:msg="+b64("rate limited"))

	parser := NewParser("%3")
	events := parser.Feed([]byte(fixture))
	if len(events) != 1 {
		t.Fatalf("events = %+v, want only the 7501 report", events)
	}
	if events[0].State != StateError || events[0].Message != "rate limited" {
		t.Fatalf("event = %+v, want error with message", events[0])
	}
}

func TestParserDropsPayloadWithoutState(t *testing.T) {
	for _, payload := range []string{
		"app=pi",
		":",
		"=junk",
		"msg=" + b64("no state here"),
	} {
		parser := NewParser("%4")
		if events := parser.Feed([]byte(stReport(payload))); len(events) != 0 {
			t.Fatalf("payload %q: events = %+v, want none", payload, events)
		}
	}
}

func TestParserDecodesBase64MessageWithUnicode(t *testing.T) {
	message := "line one\nünïcode ✓"
	parser := NewParser("%5")
	events := parser.Feed([]byte(stReport("state=error:app=pi:msg=" + b64(message))))
	if len(events) != 1 || events[0].Message != message {
		t.Fatalf("events = %+v, want message %q", events, message)
	}
}

func TestParserKeepsRawBase64WhenUndecodable(t *testing.T) {
	parser := NewParser("%6")
	events := parser.Feed([]byte(stReport("state=working:app=pi:msg=%%%not-base64%%%")))
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one report", events)
	}
	if events[0].Message != "" {
		t.Fatalf("message = %q, want empty for undecodable base64", events[0].Message)
	}
	if events[0].Values["msg"] != "%%%not-base64%%%" {
		t.Fatalf("raw value = %q, want preserved", events[0].Values["msg"])
	}
}

func TestParserKeepsUnknownKeysAndStates(t *testing.T) {
	parser := NewParser("%8")
	events := parser.Feed([]byte(stReport("state=paused:app=pi:future=1:bare")))
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one report", events)
	}
	event := events[0]
	if event.State != State("paused") {
		t.Fatalf("state = %q, want unknown value preserved", event.State)
	}
	if event.Values["future"] != "1" {
		t.Fatalf("future key = %q, want 1", event.Values["future"])
	}
	if _, ok := event.Values["bare"]; !ok {
		t.Fatalf("bare key missing from values: %+v", event.Values)
	}
}

func TestParserOversizedPayloadDroppedAndResyncs(t *testing.T) {
	huge := strings.Repeat("a", maxBodyBytes+10)
	fixture := esc + "]7501;" + huge + st +
		stReport("state=done")

	parser := NewParser("%9")
	events := parser.Feed([]byte(fixture))
	if len(events) != 1 || events[0].State != StateDone {
		t.Fatalf("events = %+v, want only the report after the oversized payload", events)
	}
}

// A malformed ESC inside a payload closes that sequence; the bytes after it
// are reprocessed so a following report is not lost.
func TestParserESCInsideBodyResyncs(t *testing.T) {
	parser := NewParser("%10")
	events := parser.Feed([]byte(esc + "]7501;state=working" + esc + "]7501;state=done" + st))
	if got := states(events); len(got) != 2 || got[0] != StateWorking || got[1] != StateDone {
		t.Fatalf("states = %v, want [working done]", got)
	}
}

func TestParserResetDropsPartialSequence(t *testing.T) {
	parser := NewParser("%11")
	if events := parser.Feed([]byte(esc + "]7501;state=work")); len(events) != 0 {
		t.Fatalf("partial feed produced %+v", events)
	}
	parser.Reset()
	events := parser.Feed([]byte(stReport("state=idle")))
	if len(events) != 1 || events[0].State != StateIdle {
		t.Fatalf("events = %+v, want the fresh idle report only", events)
	}
}

func TestParserZeroValueIsSafe(t *testing.T) {
	var parser Parser
	events := parser.Feed([]byte(stReport("state=idle")))
	if len(events) != 1 || events[0].State != StateIdle {
		t.Fatalf("zero-value parser events = %+v, want one idle report", events)
	}
	if events[0].ObservedAt.IsZero() {
		t.Fatal("zero-value parser left ObservedAt unset")
	}
}

func TestParserEmptyFeedIsSafe(t *testing.T) {
	parser := NewParser("")
	if events := parser.Feed(nil); events != nil {
		t.Fatalf("nil feed = %+v, want nil", events)
	}
	if events := parser.Feed([]byte{}); len(events) != 0 {
		t.Fatalf("empty feed = %+v, want none", events)
	}
}

func TestParserAcceptsMissingPaneID(t *testing.T) {
	parser := NewParser("")
	events := parser.Feed([]byte(stReport("state=idle")))
	if len(events) != 1 || events[0].PaneID != "" {
		t.Fatalf("events = %+v, want one event with empty pane", events)
	}
}

// FuzzParserNoPanic ensures arbitrary byte streams cannot panic or wedge the
// incremental parser. Seed corpora cover the malformed/truncated shapes
// observed in tests; run with `go test -fuzz=FuzzParserNoPanic`.
func FuzzParserNoPanic(f *testing.F) {
	seeds := []string{
		"",
		stReport("state=working:app=pi"),
		belReport("state=idle"),
		esc + "]7501;?",
		esc + "]7501;state=working" + esc,
		esc + "]75010;junk",
		esc + "]0;title" + belStr + stReport("state=done"),
		esc + "[31mred",
		esc,
		"\x00\xff\xfe",
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		parser := NewParser("%fuzz")
		_ = parser.Feed(data)
		// Feeding the same data split at an arbitrary point must also be safe.
		if len(data) > 1 {
			mid := len(data) / 2
			_ = parser.Feed(data[:mid])
			_ = parser.Feed(data[mid:])
		}
	})
}
