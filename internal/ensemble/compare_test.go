package ensemble

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCompareModes(t *testing.T) {
	t.Log("TEST: TestCompareModes - starting")

	tests := []struct {
		name          string
		modesA        []string
		modesB        []string
		wantAdded     int
		wantRemoved   int
		wantUnchanged int
	}{
		{
			name:          "identical modes",
			modesA:        []string{"mode-a", "mode-b", "mode-c"},
			modesB:        []string{"mode-a", "mode-b", "mode-c"},
			wantAdded:     0,
			wantRemoved:   0,
			wantUnchanged: 3,
		},
		{
			name:          "added mode",
			modesA:        []string{"mode-a", "mode-b"},
			modesB:        []string{"mode-a", "mode-b", "mode-c"},
			wantAdded:     1,
			wantRemoved:   0,
			wantUnchanged: 2,
		},
		{
			name:          "removed mode",
			modesA:        []string{"mode-a", "mode-b", "mode-c"},
			modesB:        []string{"mode-a", "mode-b"},
			wantAdded:     0,
			wantRemoved:   1,
			wantUnchanged: 2,
		},
		{
			name:          "mixed changes",
			modesA:        []string{"mode-a", "mode-b"},
			modesB:        []string{"mode-b", "mode-c"},
			wantAdded:     1,
			wantRemoved:   1,
			wantUnchanged: 1,
		},
		{
			name:          "completely different",
			modesA:        []string{"mode-a", "mode-b"},
			modesB:        []string{"mode-c", "mode-d"},
			wantAdded:     2,
			wantRemoved:   2,
			wantUnchanged: 0,
		},
		{
			name:          "empty runs",
			modesA:        []string{},
			modesB:        []string{},
			wantAdded:     0,
			wantRemoved:   0,
			wantUnchanged: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("TEST: %s - comparing modes A=%v vs B=%v", tc.name, tc.modesA, tc.modesB)

			diff := compareModes(tc.modesA, tc.modesB)

			t.Logf("TEST: %s - got diff: added=%d, removed=%d, unchanged=%d",
				tc.name, diff.AddedCount, diff.RemovedCount, diff.UnchangedCount)

			if diff.AddedCount != tc.wantAdded {
				t.Errorf("AddedCount = %d, want %d", diff.AddedCount, tc.wantAdded)
			}
			if diff.RemovedCount != tc.wantRemoved {
				t.Errorf("RemovedCount = %d, want %d", diff.RemovedCount, tc.wantRemoved)
			}
			if diff.UnchangedCount != tc.wantUnchanged {
				t.Errorf("UnchangedCount = %d, want %d", diff.UnchangedCount, tc.wantUnchanged)
			}
		})
	}
}

func TestCompareFindings(t *testing.T) {
	t.Log("TEST: TestCompareFindings - starting")

	outputsA := []ModeOutput{
		{
			ModeID: "mode-a",
			Thesis: "Thesis A",
			TopFindings: []Finding{
				{Finding: "Finding 1", Impact: ImpactHigh, Confidence: 0.8},
				{Finding: "Finding 2", Impact: ImpactMedium, Confidence: 0.7},
			},
		},
	}

	outputsB := []ModeOutput{
		{
			ModeID: "mode-a",
			Thesis: "Thesis A",
			TopFindings: []Finding{
				{Finding: "Finding 1", Impact: ImpactHigh, Confidence: 0.8},
				{Finding: "Finding 3", Impact: ImpactLow, Confidence: 0.6},
			},
		},
	}

	t.Logf("TEST: TestCompareFindings - comparing outputs A with %d findings vs B with %d findings",
		len(outputsA[0].TopFindings), len(outputsB[0].TopFindings))

	diff := compareFindings(outputsA, outputsB)

	t.Logf("TEST: TestCompareFindings - got diff: new=%d, missing=%d, changed=%d, unchanged=%d",
		diff.NewCount, diff.MissingCount, diff.ChangedCount, diff.UnchangedCount)

	// Finding 1 should be unchanged
	if diff.UnchangedCount != 1 {
		t.Errorf("UnchangedCount = %d, want 1", diff.UnchangedCount)
	}

	// Finding 2 should be missing
	if diff.MissingCount != 1 {
		t.Errorf("MissingCount = %d, want 1", diff.MissingCount)
	}

	// Finding 3 should be new
	if diff.NewCount != 1 {
		t.Errorf("NewCount = %d, want 1", diff.NewCount)
	}
}

func TestCompareFindingsWithChanges(t *testing.T) {
	t.Log("TEST: TestCompareFindingsWithChanges - starting")

	// Same finding text but different attributes
	outputsA := []ModeOutput{
		{
			ModeID: "mode-a",
			Thesis: "Thesis A",
			TopFindings: []Finding{
				{Finding: "Shared finding", Impact: ImpactHigh, Confidence: 0.8},
			},
		},
	}

	outputsB := []ModeOutput{
		{
			ModeID: "mode-a",
			Thesis: "Thesis A",
			TopFindings: []Finding{
				{Finding: "Shared finding", Impact: ImpactMedium, Confidence: 0.6},
			},
		},
	}

	t.Logf("TEST: TestCompareFindingsWithChanges - comparing same finding with different attributes")

	diff := compareFindings(outputsA, outputsB)

	t.Logf("TEST: TestCompareFindingsWithChanges - got diff: changed=%d", diff.ChangedCount)

	// Same finding text with different attributes should be counted as changed
	if diff.ChangedCount != 1 {
		t.Errorf("ChangedCount = %d, want 1", diff.ChangedCount)
	}

	if len(diff.Changed) > 0 {
		change := diff.Changed[0]
		t.Logf("TEST: TestCompareFindingsWithChanges - changes: %v", change.Changes)

		// Should detect impact and confidence changes
		hasImpactChange := false
		hasConfidenceChange := false
		for _, c := range change.Changes {
			if c == "impact: high -> medium" {
				hasImpactChange = true
			}
			if c == "confidence: 0.80 -> 0.60" {
				hasConfidenceChange = true
			}
		}

		if !hasImpactChange {
			t.Error("Expected impact change to be detected")
		}
		if !hasConfidenceChange {
			t.Error("Expected confidence change to be detected")
		}
	}
}

func TestCompareConclusions(t *testing.T) {
	t.Log("TEST: TestCompareConclusions - starting")

	runA := CompareInput{
		RunID: "run-a",
		Outputs: []ModeOutput{
			{ModeID: "mode-a", Thesis: "Original thesis"},
		},
		SynthesisOutput: "Original synthesis",
	}

	runB := CompareInput{
		RunID: "run-b",
		Outputs: []ModeOutput{
			{ModeID: "mode-a", Thesis: "Changed thesis"},
		},
		SynthesisOutput: "Changed synthesis",
	}

	t.Logf("TEST: TestCompareConclusions - comparing conclusions")

	diff := compareConclusions(runA, runB)

	t.Logf("TEST: TestCompareConclusions - thesis changes=%d, synthesis changed=%v",
		len(diff.ThesisChanges), diff.SynthesisChanged)

	if len(diff.ThesisChanges) != 1 {
		t.Errorf("Expected 1 thesis change, got %d", len(diff.ThesisChanges))
	}

	if !diff.SynthesisChanged {
		t.Error("Expected synthesis to be marked as changed")
	}
}

func TestCompareContributions(t *testing.T) {
	t.Log("TEST: TestCompareContributions - starting")

	reportA := &ContributionReport{
		GeneratedAt: time.Now(),
		Scores: []ContributionScore{
			{ModeID: "mode-a", Score: 50.0, Rank: 1},
			{ModeID: "mode-b", Score: 30.0, Rank: 2},
		},
		OverlapRate:    0.2,
		DiversityScore: 0.8,
	}

	reportB := &ContributionReport{
		GeneratedAt: time.Now(),
		Scores: []ContributionScore{
			{ModeID: "mode-a", Score: 40.0, Rank: 2},
			{ModeID: "mode-b", Score: 45.0, Rank: 1},
		},
		OverlapRate:    0.3,
		DiversityScore: 0.7,
	}

	t.Logf("TEST: TestCompareContributions - comparing contribution reports")

	diff := compareContributions(reportA, reportB)

	t.Logf("TEST: TestCompareContributions - score deltas=%d, rank changes=%d",
		len(diff.ScoreDeltas), len(diff.RankChanges))

	// Both modes should have score deltas
	if len(diff.ScoreDeltas) != 2 {
		t.Errorf("Expected 2 score deltas, got %d", len(diff.ScoreDeltas))
	}

	// Both modes should have rank changes
	if len(diff.RankChanges) != 2 {
		t.Errorf("Expected 2 rank changes, got %d", len(diff.RankChanges))
	}

	// Check overlap rate was captured
	if diff.OverlapRateA != 0.2 || diff.OverlapRateB != 0.3 {
		t.Errorf("Overlap rates not captured correctly: A=%f, B=%f", diff.OverlapRateA, diff.OverlapRateB)
	}
}

func TestCompare_FullIntegration(t *testing.T) {
	t.Log("TEST: TestCompare_FullIntegration - starting")

	runA := CompareInput{
		RunID:   "run-alpha",
		ModeIDs: []string{"deductive", "bayesian"},
		Outputs: []ModeOutput{
			{
				ModeID: "deductive",
				Thesis: "Deductive conclusion A",
				TopFindings: []Finding{
					{Finding: "Shared finding", Impact: ImpactHigh, Confidence: 0.9},
					{Finding: "Deductive only A", Impact: ImpactMedium, Confidence: 0.8},
				},
				GeneratedAt: time.Now(),
			},
			{
				ModeID: "bayesian",
				Thesis: "Bayesian conclusion A",
				TopFindings: []Finding{
					{Finding: "Bayesian finding A", Impact: ImpactMedium, Confidence: 0.7},
				},
				GeneratedAt: time.Now(),
			},
		},
		Contributions: &ContributionReport{
			Scores: []ContributionScore{
				{ModeID: "deductive", Score: 60.0, Rank: 1},
				{ModeID: "bayesian", Score: 40.0, Rank: 2},
			},
		},
		SynthesisOutput: "Combined analysis A",
	}

	runB := CompareInput{
		RunID:   "run-beta",
		ModeIDs: []string{"deductive", "causal"},
		Outputs: []ModeOutput{
			{
				ModeID: "deductive",
				Thesis: "Deductive conclusion B",
				TopFindings: []Finding{
					{Finding: "Shared finding", Impact: ImpactHigh, Confidence: 0.9},
					{Finding: "Deductive only B", Impact: ImpactHigh, Confidence: 0.85},
				},
				GeneratedAt: time.Now(),
			},
			{
				ModeID: "causal",
				Thesis: "Causal conclusion B",
				TopFindings: []Finding{
					{Finding: "Causal finding B", Impact: ImpactCritical, Confidence: 0.95},
				},
				GeneratedAt: time.Now(),
			},
		},
		Contributions: &ContributionReport{
			Scores: []ContributionScore{
				{ModeID: "deductive", Score: 55.0, Rank: 1},
				{ModeID: "causal", Score: 45.0, Rank: 2},
			},
		},
		SynthesisOutput: "Combined analysis B",
	}

	t.Logf("TEST: TestCompare_FullIntegration - comparing %s vs %s", runA.RunID, runB.RunID)

	result := Compare(runA, runB)

	t.Logf("TEST: TestCompare_FullIntegration - result summary: %s", result.Summary)

	// Mode changes: +causal, -bayesian, =deductive
	if result.ModeDiff.AddedCount != 1 {
		t.Errorf("Expected 1 added mode, got %d", result.ModeDiff.AddedCount)
	}
	if result.ModeDiff.RemovedCount != 1 {
		t.Errorf("Expected 1 removed mode, got %d", result.ModeDiff.RemovedCount)
	}
	if result.ModeDiff.UnchangedCount != 1 {
		t.Errorf("Expected 1 unchanged mode, got %d", result.ModeDiff.UnchangedCount)
	}

	// Finding changes
	if result.FindingsDiff.UnchangedCount != 1 {
		t.Errorf("Expected 1 unchanged finding (shared), got %d", result.FindingsDiff.UnchangedCount)
	}

	// Conclusions should have changed
	if !result.ConclusionDiff.SynthesisChanged {
		t.Error("Expected synthesis to be marked as changed")
	}

	// Contribution changes (only deductive is in both, so only 1 score delta)
	if len(result.ContributionDiff.ScoreDeltas) != 1 {
		t.Errorf("Expected 1 score delta (deductive), got %d", len(result.ContributionDiff.ScoreDeltas))
	}
}

func TestCompare_Determinism(t *testing.T) {
	t.Log("TEST: TestCompare_Determinism - starting")

	runA := CompareInput{
		RunID:   "run-a",
		ModeIDs: []string{"z-mode", "a-mode", "m-mode"},
		Outputs: []ModeOutput{
			{ModeID: "z-mode", Thesis: "Z", TopFindings: []Finding{{Finding: "Z finding", Impact: ImpactMedium, Confidence: 0.7}}},
			{ModeID: "a-mode", Thesis: "A", TopFindings: []Finding{{Finding: "A finding", Impact: ImpactHigh, Confidence: 0.8}}},
			{ModeID: "m-mode", Thesis: "M", TopFindings: []Finding{{Finding: "M finding", Impact: ImpactLow, Confidence: 0.6}}},
		},
	}

	runB := CompareInput{
		RunID:   "run-b",
		ModeIDs: []string{"z-mode", "a-mode", "b-mode"},
		Outputs: []ModeOutput{
			{ModeID: "z-mode", Thesis: "Z", TopFindings: []Finding{{Finding: "Z finding", Impact: ImpactMedium, Confidence: 0.7}}},
			{ModeID: "a-mode", Thesis: "A", TopFindings: []Finding{{Finding: "A finding", Impact: ImpactHigh, Confidence: 0.8}}},
			{ModeID: "b-mode", Thesis: "B", TopFindings: []Finding{{Finding: "B finding", Impact: ImpactCritical, Confidence: 0.9}}},
		},
	}

	t.Logf("TEST: TestCompare_Determinism - running comparison twice")

	result1 := Compare(runA, runB)
	result2 := Compare(runA, runB)

	// Compare JSON outputs for determinism
	json1, _ := json.MarshalIndent(result1, "", "  ")
	json2, _ := json.MarshalIndent(result2, "", "  ")

	// Parse back to remove timestamp fields
	var parsed1, parsed2 map[string]interface{}
	json.Unmarshal(json1, &parsed1)
	json.Unmarshal(json2, &parsed2)

	// Remove timestamps
	delete(parsed1, "generated_at")
	delete(parsed2, "generated_at")

	// Re-serialize
	cleaned1, _ := json.Marshal(parsed1)
	cleaned2, _ := json.Marshal(parsed2)

	t.Logf("TEST: TestCompare_Determinism - comparing outputs")

	if string(cleaned1) != string(cleaned2) {
		t.Errorf("Comparison results are not deterministic")
	}
}

func TestCompare_EmptyInputs(t *testing.T) {
	t.Log("TEST: TestCompare_EmptyInputs - starting")

	runA := CompareInput{RunID: "empty-a"}
	runB := CompareInput{RunID: "empty-b"}

	t.Logf("TEST: TestCompare_EmptyInputs - comparing empty runs")

	result := Compare(runA, runB)

	t.Logf("TEST: TestCompare_EmptyInputs - summary: %s", result.Summary)

	if result.Summary != "No differences found" {
		t.Errorf("Expected 'No differences found', got %q", result.Summary)
	}
}

func TestCompare_NilContributions(t *testing.T) {
	t.Log("TEST: TestCompare_NilContributions - starting")

	runA := CompareInput{
		RunID:         "run-a",
		Contributions: nil,
	}
	runB := CompareInput{
		RunID:         "run-b",
		Contributions: nil,
	}

	t.Logf("TEST: TestCompare_NilContributions - comparing runs with nil contributions")

	result := Compare(runA, runB)

	// Should not panic and should return empty contribution diff
	if len(result.ContributionDiff.ScoreDeltas) != 0 {
		t.Errorf("Expected 0 score deltas for nil contributions, got %d", len(result.ContributionDiff.ScoreDeltas))
	}
}

func TestCompare_ModeDiff(t *testing.T) {
	input := map[string]any{"runA": []string{"mode-a", "mode-b"}, "runB": []string{"mode-b", "mode-c"}}
	logTestStartCompare(t, input)

	result := Compare(
		CompareInput{RunID: "A", ModeIDs: []string{"mode-a", "mode-b"}},
		CompareInput{RunID: "B", ModeIDs: []string{"mode-b", "mode-c"}},
	)
	logTestResultCompare(t, result.ModeDiff)

	assertEqualCompare(t, "added count", result.ModeDiff.AddedCount, 1)
	assertEqualCompare(t, "removed count", result.ModeDiff.RemovedCount, 1)
	assertTrueCompare(t, "unchanged includes mode-b", containsStringCompare(result.ModeDiff.Unchanged, "mode-b"))
}

func TestCompare_FindingDiff(t *testing.T) {
	input := map[string]any{"runA": "finding-a", "runB": "finding-b"}
	logTestStartCompare(t, input)

	runA := CompareInput{
		RunID:   "A",
		Outputs: []ModeOutput{{ModeID: "mode-a", TopFindings: []Finding{{Finding: "finding-a", Impact: ImpactHigh, Confidence: 0.8}}}},
	}
	runB := CompareInput{
		RunID:   "B",
		Outputs: []ModeOutput{{ModeID: "mode-a", TopFindings: []Finding{{Finding: "finding-b", Impact: ImpactHigh, Confidence: 0.8}}}},
	}
	result := Compare(runA, runB)
	logTestResultCompare(t, result.FindingsDiff)

	assertEqualCompare(t, "new count", result.FindingsDiff.NewCount, 1)
	assertEqualCompare(t, "missing count", result.FindingsDiff.MissingCount, 1)
}

func TestCompare_ContributionDiff(t *testing.T) {
	input := map[string]any{"runA": 10.0, "runB": 20.0}
	logTestStartCompare(t, input)

	contribA := &ContributionReport{
		Scores:         []ContributionScore{{ModeID: "mode-a", Score: 10, Rank: 1}},
		OverlapRate:    0.2,
		DiversityScore: 0.5,
	}
	contribB := &ContributionReport{
		Scores:         []ContributionScore{{ModeID: "mode-a", Score: 20, Rank: 1}},
		OverlapRate:    0.3,
		DiversityScore: 0.6,
	}

	result := Compare(CompareInput{RunID: "A", Contributions: contribA}, CompareInput{RunID: "B", Contributions: contribB})
	logTestResultCompare(t, result.ContributionDiff)

	assertEqualCompare(t, "score delta count", len(result.ContributionDiff.ScoreDeltas), 1)
	assertEqualCompare(t, "overlap rate B", result.ContributionDiff.OverlapRateB, 0.3)
}

func logTestStartCompare(t *testing.T, input any) {
	t.Helper()
	t.Logf("TEST: %s - starting with input: %v", t.Name(), input)
}

func logTestResultCompare(t *testing.T, result any) {
	t.Helper()
	t.Logf("TEST: %s - got result: %v", t.Name(), result)
}

func assertTrueCompare(t *testing.T, desc string, ok bool) {
	t.Helper()
	t.Logf("TEST: %s - assertion: %s", t.Name(), desc)
	if !ok {
		t.Fatalf("assertion failed: %s", desc)
	}
}

func assertEqualCompare(t *testing.T, desc string, got, want any) {
	t.Helper()
	t.Logf("TEST: %s - assertion: %s", t.Name(), desc)
	if got != want {
		t.Fatalf("%s: got %v want %v", desc, got, want)
	}
}

func containsStringCompare(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func TestFormatComparison(t *testing.T) {
	t.Log("TEST: TestFormatComparison - starting")

	result := &ComparisonResult{
		RunA:        "run-alpha",
		RunB:        "run-beta",
		GeneratedAt: time.Now(),
		ModeDiff: ModeDiff{
			Added:          []string{"causal"},
			Removed:        []string{"bayesian"},
			Unchanged:      []string{"deductive"},
			AddedCount:     1,
			RemovedCount:   1,
			UnchangedCount: 1,
		},
		FindingsDiff: FindingsDiff{
			New:          []FindingDiffEntry{{FindingID: "abc123", ModeID: "causal", Text: "New finding"}},
			Missing:      []FindingDiffEntry{{FindingID: "def456", ModeID: "bayesian", Text: "Missing finding"}},
			NewCount:     1,
			MissingCount: 1,
		},
		Summary: "+1 modes, -1 modes, +1 findings, -1 findings",
	}

	t.Logf("TEST: TestFormatComparison - formatting result")

	formatted := FormatComparison(result)

	t.Logf("TEST: TestFormatComparison - output length: %d chars", len(formatted))

	// Check key sections are present
	if len(formatted) == 0 {
		t.Error("Formatted output should not be empty")
	}

	if !compareContainsAll(formatted, []string{"Ensemble Comparison", "run-alpha", "run-beta", "Mode Changes", "Finding Changes"}) {
		t.Error("Formatted output missing expected sections")
	}
}

func TestBuildFindingMap_Determinism(t *testing.T) {
	t.Log("TEST: TestBuildFindingMap_Determinism - starting")

	outputs := []ModeOutput{
		{
			ModeID: "mode-a",
			TopFindings: []Finding{
				{Finding: "Finding 1", Impact: ImpactHigh, Confidence: 0.8},
				{Finding: "Finding 2", Impact: ImpactMedium, Confidence: 0.7},
			},
		},
	}

	map1 := buildFindingMap(outputs)
	map2 := buildFindingMap(outputs)

	t.Logf("TEST: TestBuildFindingMap_Determinism - built maps with %d entries each", len(map1))

	// Same inputs should produce same FindingIDs
	for id := range map1 {
		if _, exists := map2[id]; !exists {
			t.Errorf("FindingID %s not found in second map", id)
		}
	}

	// Verify FindingID is deterministic
	id1 := GenerateFindingID("mode-a", "Finding 1")
	id2 := GenerateFindingID("mode-a", "Finding 1")
	if id1 != id2 {
		t.Errorf("GenerateFindingID not deterministic: %s != %s", id1, id2)
	}
}

// compareContainsAll checks if s contains all substrings in subs.
func compareContainsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !compareContainsStr(s, sub) {
			return false
		}
	}
	return true
}

// compareContainsStr checks if s contains sub (simple helper to avoid import).
func compareContainsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && compareFindSubstring(s, sub)))
}

func compareFindSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestAbsInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input int
		want  int
	}{
		{0, 0},
		{1, 1},
		{-1, 1},
		{42, 42},
		{-42, 42},
		{-2147483648, 2147483648}, // min int32 (on 64-bit)
	}

	for _, tc := range tests {
		if got := absInt(tc.input); got != tc.want {
			t.Errorf("absInt(%d) = %d, want %d", tc.input, got, tc.want)
		}
	}
}
