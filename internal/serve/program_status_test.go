package serve

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

func oscReport(pairs string) string { return "\x1b]7501;" + pairs + "\x1b\\" }

// TestObservePaneProgramStatusReassemblesSplitLines proves the raw stream
// capture feeds the observer with the pane target and that a report split
// across two stream events is reassembled (the parser is incremental).
// publishTestStreamEvent drives the same decode + publish path the tap handler
// uses, so tests exercise the real production fan-out without a live stream.
func publishTestStreamEvent(srv *Server, event tmux.StreamEvent) {
	srv.publishPaneStreamEvent(event, srv.programStatusTap.ObserveEvent(event))
}

func TestObservePaneProgramStatusReassemblesSplitLines(t *testing.T) {
	srv, _ := setupTestServer(t)

	publishTestStreamEvent(srv, tmux.StreamEvent{
		Target: "demo:0",
		Lines:  []string{"\x1b]7501;state=work"},
	})
	if _, ok := srv.programStatusTap.Observer().Latest("demo:0"); ok {
		t.Fatal("incomplete report stored")
	}

	publishTestStreamEvent(srv, tmux.StreamEvent{
		Target: "demo:0",
		Lines:  []string{"ing:app=pi:msg=" + base64.StdEncoding.EncodeToString([]byte("session-name")) + "\x1b\\"},
	})

	event, ok := srv.programStatusTap.Observer().Latest("demo:0")
	if !ok {
		t.Fatal("reassembled report not stored")
	}
	if event.State != "working" || event.App != "pi" || event.Message != "session-name" {
		t.Fatalf("stored report = %+v, want working/pi/session-name", event)
	}
}

// TestObservePaneProgramStatusIgnoresScrapedCaptures pins the no-scrape
// contract: polling events carry rendered screen content, which can never
// contain OSC sequences, and must not be parsed as a status source.
func TestObservePaneProgramStatusIgnoresScrapedCaptures(t *testing.T) {
	srv, _ := setupTestServer(t)

	publishTestStreamEvent(srv, tmux.StreamEvent{
		Target: "demo:0",
		Lines:  []string{oscReport("state=done:app=pi")},
		IsFull: true,
	})
	if _, ok := srv.programStatusTap.Observer().Latest("demo:0"); ok {
		t.Fatal("scraped capture was treated as an OSC status")
	}

	publishTestStreamEvent(srv, tmux.StreamEvent{
		Target: "demo:0",
		Lines:  []string{oscReport("state=done:app=pi")},
		IsFull: false,
	})
	if event, ok := srv.programStatusTap.Observer().Latest("demo:0"); !ok || event.State != "done" {
		t.Fatalf("pipe event not stored: %+v, %v", event, ok)
	}
}

func TestHandleProgramStatusV1ReturnsStoredReports(t *testing.T) {
	srv, _ := setupTestServer(t)

	publishTestStreamEvent(srv, tmux.StreamEvent{Target: "demo:1", Lines: []string{oscReport("state=error:app=pi:msg=" + base64.StdEncoding.EncodeToString([]byte("boom")))}})
	publishTestStreamEvent(srv, tmux.StreamEvent{Target: "demo:0", Lines: []string{oscReport("state=done:app=pi")}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/streaming/program-status", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success  bool `json:"success"`
		Count    int  `json:"count"`
		Statuses []struct {
			Pane    string `json:"pane"`
			State   string `json:"state"`
			App     string `json:"app"`
			Message string `json:"message"`
		} `json:"statuses"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success || resp.Count != 2 || len(resp.Statuses) != 2 {
		t.Fatalf("resp = %+v, want 2 statuses", resp)
	}
	// Sorted by pane target for determinism.
	if resp.Statuses[0].Pane != "demo:0" || resp.Statuses[0].State != "done" {
		t.Fatalf("first status = %+v, want demo:0 done", resp.Statuses[0])
	}
	if resp.Statuses[1].Pane != "demo:1" || resp.Statuses[1].State != "error" || resp.Statuses[1].Message != "boom" {
		t.Fatalf("second status = %+v, want demo:1 error boom", resp.Statuses[1])
	}
}

func TestHandleProgramStatusV1FiltersByPaneAndNeverNulls(t *testing.T) {
	srv, _ := setupTestServer(t)

	publishTestStreamEvent(srv, tmux.StreamEvent{Target: "demo:0", Lines: []string{oscReport("state=done:app=pi")}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/streaming/program-status?pane=demo:0", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var filtered map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if filtered["count"].(float64) != 1 {
		t.Fatalf("filtered count = %v, want 1", filtered["count"])
	}

	// An unmatched filter still returns an array, never null.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/streaming/program-status?pane=absent:9", nil)
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var empty struct {
		Statuses []json.RawMessage `json:"statuses"`
	}
	if err := json.Unmarshal([]byte(body), &empty); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if empty.Statuses == nil || len(empty.Statuses) != 0 {
		t.Fatalf("statuses = %v, want empty array", empty.Statuses)
	}
}
