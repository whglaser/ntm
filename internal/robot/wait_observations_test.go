package robot

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWaitPaneObservationsRetainUnreadableTargets(t *testing.T) {
	idle := []*AgentActivity{{PaneID: "%1", State: StateWaiting, AgentType: "claude"}}
	for _, tc := range []struct {
		name        string
		opts        WaitOptions
		conditions  []string
		activities  []*AgentActivity
		wantMet     bool
		wantPending []string
	}{
		{
			name:       "all does not silently drop a failed capture",
			conditions: []string{WaitConditionIdle}, activities: idle,
			wantPending: []string{"%2"},
		},
		{
			name:       "any still succeeds with positive evidence",
			opts:       WaitOptions{WaitForAny: true, CountN: 1},
			conditions: []string{WaitConditionIdle}, activities: idle,
			wantMet: true, wantPending: []string{"%2"},
		},
		{
			name:       "count never includes unreadable panes",
			opts:       WaitOptions{WaitForAny: true, CountN: 2},
			conditions: []string{WaitConditionIdle}, activities: idle,
			wantPending: []string{"%2"},
		},
		{
			name:       "exit on error does not certify an unreadable pane",
			opts:       WaitOptions{WaitForAny: true, CountN: 1, ExitOnError: true},
			conditions: []string{WaitConditionIdle}, activities: idle,
			wantPending: []string{"%2"},
		},
		{
			name: "attention only still honors error gate",
			opts: WaitOptions{ExitOnError: true}, activities: idle,
			wantPending: []string{"%2"},
		},
		{
			name: "attention error gate cannot be bypassed by any",
			opts: WaitOptions{WaitForAny: true, CountN: 1, ExitOnError: true}, activities: idle,
			wantPending: []string{"%2"},
		},
		{
			name:       "attention only does not invent a pane condition",
			activities: idle, wantMet: true,
		},
		{
			name:       "all captures failed remains pending",
			conditions: []string{WaitConditionIdle}, wantPending: []string{"%2"},
		},
		{
			name:       "negative healthy predicate cannot certify failed capture",
			conditions: []string{WaitConditionHealthy}, activities: idle,
			wantPending: []string{"%2"},
		},
		{
			name:       "rate limit lifted needs an observation of every target",
			conditions: []string{WaitConditionRateLimitLifted}, activities: idle,
			wantPending: []string{"%2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			met, matching, pending := checkWaitPaneObservations(tc.activities, []string{"%2"}, tc.opts, tc.conditions, nil, nil)
			if met != tc.wantMet || !reflect.DeepEqual(pending, tc.wantPending) {
				t.Fatalf("met=%v pending=%v, want met=%v pending=%v", met, pending, tc.wantMet, tc.wantPending)
			}
			for _, match := range matching {
				if match.Pane == "%2" {
					t.Fatal("fabricated a state for the unreadable target")
				}
			}
		})
	}
}

func TestWaitPaneObservationsRecoverAfterCaptureFailure(t *testing.T) {
	activities := []*AgentActivity{{PaneID: "%1", State: StateWaiting}}
	opts := WaitOptions{}
	conditions := []string{WaitConditionIdle}
	met, _, pending := checkWaitPaneObservations(activities, []string{"%2"}, opts, conditions, nil, nil)
	if met || len(pending) != 1 {
		t.Fatalf("failed capture did not block: met=%v pending=%v", met, pending)
	}
	activities = append(activities, &AgentActivity{PaneID: "%2", State: StateWaiting})
	met, matching, pending := checkWaitPaneObservations(activities, nil, opts, conditions, nil, nil)
	if !met || len(matching) != 2 || len(pending) != 0 {
		t.Fatalf("successful recovery remained blocked: met=%v matching=%v pending=%v", met, matching, pending)
	}
}

func TestWaitTransitionsStartAtEachPanesFirstObservation(t *testing.T) {
	initial := make(map[string]bool)
	left := make(map[string]bool)
	conditions := []string{WaitConditionIdle}
	opts := WaitOptions{RequireTransition: true}
	check := func(activities []*AgentActivity, missing []string, wantMet bool) {
		t.Helper()
		recordWaitTransitions(activities, conditions, initial, left)
		met, _, pending := checkWaitPaneObservations(activities, missing, opts, conditions, initial, left)
		if met != wantMet {
			t.Fatalf("met=%v want=%v pending=%v initial=%v left=%v", met, wantMet, pending, initial, left)
		}
	}

	// No initial observation, then first successful capture already idle.
	check(nil, []string{"%2"}, false)
	idle := []*AgentActivity{{PaneID: "%2", State: StateWaiting}}
	check(idle, nil, false)
	// Losing capture is not evidence of leaving the target state.
	check(nil, []string{"%2"}, false)
	check(idle, nil, false)
	check([]*AgentActivity{{PaneID: "%2", State: StateGenerating}}, nil, false)
	check(idle, nil, true)
}

func TestWaitTransitionsInitiallyBusyNeedsOnlyOneArrival(t *testing.T) {
	initial := make(map[string]bool)
	left := make(map[string]bool)
	conditions := []string{WaitConditionIdle}
	recordWaitTransitions([]*AgentActivity{{PaneID: "%3", State: StateGenerating}}, conditions, initial, left)
	idle := []*AgentActivity{{PaneID: "%3", State: StateWaiting}}
	recordWaitTransitions(idle, conditions, initial, left)
	met, _, pending := checkWaitPaneObservations(idle, nil, WaitOptions{RequireTransition: true}, conditions, initial, left)
	if !met || len(pending) != 0 {
		t.Fatalf("initially busy pane needs no extra cycle: met=%v pending=%v", met, pending)
	}
}

// TestPendingWaitEvidenceNamesDecider covers ntm-l6cuz.7's pending-pane
// evidence: the deciding patterns, state source and state age travel with the
// pane, unobserved panes are marked unknown, and panes outside the target set
// never appear.
func TestPendingWaitEvidenceNamesDecider(t *testing.T) {
	since := time.Now().Add(-90 * time.Second)
	activities := []*AgentActivity{
		{PaneID: "%7", State: StateThinking, StateSource: "scrape", StateSince: since, AgentType: "pi", DetectedPatterns: []string{"plugin_idle", "dots_spinner"}},
		{PaneID: "%8", State: StateWaiting, StateSource: "scrape", AgentType: "pi"},
	}
	evidence := pendingWaitEvidence(activities, []string{"%9"}, []string{"%7", "%9"})
	if len(evidence) != 2 {
		t.Fatalf("pending evidence = %+v, want two entries", evidence)
	}
	decider := evidence[0]
	if decider.Pane != "%7" || decider.State != string(StateThinking) || decider.StateSource != "scrape" || decider.StateSince == "" {
		t.Fatalf("decider evidence = %+v", decider)
	}
	if len(decider.DetectedPatterns) != 2 || decider.DetectedPatterns[0] != "plugin_idle" {
		t.Fatalf("deciding patterns = %v, want plugin_idle and dots_spinner", decider.DetectedPatterns)
	}
	if unobserved := evidence[1]; unobserved.Pane != "%9" || unobserved.State != "unknown" {
		t.Fatalf("unobserved evidence = %+v, want unknown", unobserved)
	}
}

// TestWaitResponsePendingEvidenceJSONShape pins the machine-readable surface
// named in the acceptance: pending_evidence with deciding patterns/source.
func TestWaitResponsePendingEvidenceJSONShape(t *testing.T) {
	response := WaitResponse{
		Agents:        []WaitAgentInfo{},
		AgentsPending: []string{"%7"},
		PendingEvidence: []WaitAgentInfo{{
			Pane: "%7", State: "thinking", AgentType: "pi", StateSource: "scrape",
			StateSince:       time.Now().UTC().Format(time.RFC3339),
			DetectedPatterns: []string{"plugin_idle", "dots_spinner"},
		}},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal wait response: %v", err)
	}
	for _, key := range []string{`"pending_evidence"`, `"detected_patterns"`, `"state_source"`, `"state_since"`} {
		if !strings.Contains(string(encoded), key) {
			t.Fatalf("wait JSON missing %s: %s", key, encoded)
		}
	}
}
