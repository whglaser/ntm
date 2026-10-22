package bv

import (
	"encoding/json"
	"testing"
)

// The fixtures below are real response fragments from beads_viewer_rust
// (bvr 0.4.1) captured against a live workspace. bvr keeps bv's robot flags
// but changes some payload shapes, so the decoders must accept both.

func TestScoreBreakdownAcceptsBVRRows(t *testing.T) {
	raw := `[
	  {"name":"PageRank","weight":0.22,"raw":0.000185,"normalized":0.02098,"weighted":0.004616},
	  {"name":"Betweenness","weight":0.2,"raw":0,"normalized":0,"weighted":0},
	  {"name":"BlockerRatio","weight":0.13,"raw":0,"normalized":0,"weighted":0},
	  {"name":"Staleness","weight":0.05,"raw":5.77,"normalized":0.9358,"weighted":0.04679},
	  {"name":"PriorityBoost","weight":0.1,"raw":3.0,"normalized":0.4,"weighted":0.04},
	  {"name":"TimeToImpact","weight":0.1,"raw":1.0,"normalized":0.0416,"weighted":0.0041,"explanation":"critical depth 1/24"}
	]`
	var got ScoreBreakdown
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("bvr rows: %v", err)
	}
	want := ScoreBreakdown{
		Pagerank: 0.02098, Betweenness: 0, BlockerRatio: 0,
		Staleness: 0.9358, PriorityBoost: 0.4, TimeToImpact: 0.0416,
		TimeToImpactExplanation: "critical depth 1/24",
	}
	if got != want {
		t.Fatalf("bvr breakdown = %+v, want %+v", got, want)
	}
}

func TestScoreBreakdownAcceptsBVObject(t *testing.T) {
	raw := `{"pagerank":0.4,"betweenness":0.1,"blocker_ratio":0.2,"staleness":0.3,"priority_boost":0.5,"time_to_impact":0.6,"urgency":0.7,"risk":0.8,"risk_explanation":"x"}`
	var got ScoreBreakdown
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("bv object: %v", err)
	}
	if got.Pagerank != 0.4 || got.Betweenness != 0.1 || got.Risk != 0.8 || got.RiskExplanation != "x" {
		t.Fatalf("bv breakdown = %+v", got)
	}
}

func TestNodeScoreAcceptsBothBVAndBVRShapes(t *testing.T) {
	cases := map[string]NodeScore{
		// Keystones / orphans / critical path / slack are bare id strings.
		`"bd-10gyo"`: {ID: "bd-10gyo"},
		// Hubs / authorities / influencers use id+value.
		`{"id":"bd-j9jo3.9.4","value":0.3146}`: {ID: "bd-j9jo3.9.4", Value: 0.3146},
		// Bottlenecks use id+score (and carry extra fields).
		`{"id":"bd-ld4j2","title":"Burn down","score":0.000185,"blocks_count":0}`: {ID: "bd-ld4j2", Value: 0.000185},
		// bv's legacy object shape.
		`{"ID":"x","Value":1.5}`: {ID: "x", Value: 1.5},
	}
	for raw, want := range cases {
		var got NodeScore
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("NodeScore %s: %v", raw, err)
		}
		if got != want {
			t.Fatalf("NodeScore %s = %+v, want %+v", raw, got, want)
		}
	}
}

func TestInsightsResponseAcceptsBVRShapes(t *testing.T) {
	raw := `{
	  "Bottlenecks":[{"id":"bd-ld4j2","title":"t","score":0.5,"blocks_count":0}],
	  "Keystones":["bd-10gyo","bd-2d366"],
	  "Hubs":[{"id":"h","value":0.31}],
	  "Cycles":[]
	}`
	var got InsightsResponse
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("insights: %v", err)
	}
	if len(got.Bottlenecks) != 1 || got.Bottlenecks[0].ID != "bd-ld4j2" || got.Bottlenecks[0].Value != 0.5 {
		t.Fatalf("bottlenecks = %+v", got.Bottlenecks)
	}
	if len(got.Keystones) != 2 || got.Keystones[0].ID != "bd-10gyo" || got.Keystones[1].ID != "bd-2d366" {
		t.Fatalf("keystones = %+v", got.Keystones)
	}
	if len(got.Hubs) != 1 || got.Hubs[0].Value != 0.31 {
		t.Fatalf("hubs = %+v", got.Hubs)
	}
}

func TestClassifyDriftExitRecognizesBVRNoBaseline(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		message string
		want    DriftStatus
	}{
		{"bv no baseline", 1, "No baseline found", DriftNoBaseline},
		{"bvr no baseline", 1, "error: failed to read baseline: No such file or directory (os error 2)\nhint: run --save-baseline first to create a baseline", DriftNoBaseline},
		{"real critical drift", 1, "3 issues drifted from the baseline", DriftCritical},
		{"warning", 2, "warning: minor drift", DriftWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDriftExit(tc.code, tc.message)
			if got.Status != tc.want {
				t.Errorf("classifyDriftExit(%d, %q).Status = %v, want %v", tc.code, tc.message, got.Status, tc.want)
			}
		})
	}
}
