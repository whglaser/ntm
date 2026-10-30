package ensemble

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// ComparisonResult holds the deterministic diff between two ensemble runs.
type ComparisonResult struct {
	// RunA is the first run identifier (session name or run ID).
	RunA string `json:"run_a"`

	// RunB is the second run identifier.
	RunB string `json:"run_b"`

	// GeneratedAt is when this comparison was created.
	GeneratedAt time.Time `json:"generated_at"`

	// ModeDiff shows changes in mode composition.
	ModeDiff ModeDiff `json:"mode_diff"`

	// FindingsDiff shows changes in findings.
	FindingsDiff FindingsDiff `json:"findings_diff"`

	// ConclusionDiff shows changes in thesis/conclusions.
	ConclusionDiff ConclusionDiff `json:"conclusion_diff"`

	// ContributionDiff shows changes in mode contribution scores.
	ContributionDiff ContributionDiff `json:"contribution_diff"`

	// Summary provides an overall description of the diff.
	Summary string `json:"summary"`
}

// ModeDiff tracks changes in mode composition between runs.
type ModeDiff struct {
	// Added lists modes present in B but not A.
	Added []string `json:"added,omitempty"`

	// Removed lists modes present in A but not B.
	Removed []string `json:"removed,omitempty"`

	// Unchanged lists modes present in both runs.
	Unchanged []string `json:"unchanged,omitempty"`

	// AddedCount is len(Added).
	AddedCount int `json:"added_count"`

	// RemovedCount is len(Removed).
	RemovedCount int `json:"removed_count"`

	// UnchangedCount is len(Unchanged).
	UnchangedCount int `json:"unchanged_count"`
}

// FindingsDiff tracks changes in findings between runs.
type FindingsDiff struct {
	// New lists findings present in B but not A.
	New []FindingDiffEntry `json:"new,omitempty"`

	// Missing lists findings present in A but not B.
	Missing []FindingDiffEntry `json:"missing,omitempty"`

	// Changed lists findings present in both but with different attributes.
	Changed []FindingChange `json:"changed,omitempty"`

	// Unchanged lists findings present in both with same attributes.
	Unchanged []FindingDiffEntry `json:"unchanged,omitempty"`

	// NewCount is len(New).
	NewCount int `json:"new_count"`

	// MissingCount is len(Missing).
	MissingCount int `json:"missing_count"`

	// ChangedCount is len(Changed).
	ChangedCount int `json:"changed_count"`

	// UnchangedCount is len(Unchanged).
	UnchangedCount int `json:"unchanged_count"`
}

// FindingDiffEntry represents a finding in the diff.
type FindingDiffEntry struct {
	// FindingID is the stable hash identifying this finding.
	FindingID string `json:"finding_id"`

	// ModeID is the source mode.
	ModeID string `json:"mode_id"`

	// Text is the finding text.
	Text string `json:"text"`

	// Impact is the finding's impact level.
	Impact ImpactLevel `json:"impact"`

	// Confidence is the finding's confidence score.
	Confidence Confidence `json:"confidence"`
}

// FindingChange represents a finding that changed between runs.
type FindingChange struct {
	// FindingID is the stable hash identifying this finding.
	FindingID string `json:"finding_id"`

	// ModeID is the source mode.
	ModeID string `json:"mode_id"`

	// TextA is the finding text in run A.
	TextA string `json:"text_a,omitempty"`

	// TextB is the finding text in run B.
	TextB string `json:"text_b,omitempty"`

	// ImpactA is the impact in run A.
	ImpactA ImpactLevel `json:"impact_a,omitempty"`

	// ImpactB is the impact in run B.
	ImpactB ImpactLevel `json:"impact_b,omitempty"`

	// ConfidenceA is the confidence in run A.
	ConfidenceA Confidence `json:"confidence_a,omitempty"`

	// ConfidenceB is the confidence in run B.
	ConfidenceB Confidence `json:"confidence_b,omitempty"`

	// Changes describes what changed.
	Changes []string `json:"changes"`
}

// ConclusionDiff tracks changes in thesis/conclusions between runs.
type ConclusionDiff struct {
	// ThesisChanges maps mode IDs to their thesis changes.
	ThesisChanges []ThesisChange `json:"thesis_changes,omitempty"`

	// SynthesisChanged indicates if the synthesis output changed.
	SynthesisChanged bool `json:"synthesis_changed"`

	// SynthesisA is the synthesis from run A (truncated if long).
	SynthesisA string `json:"synthesis_a,omitempty"`

	// SynthesisB is the synthesis from run B (truncated if long).
	SynthesisB string `json:"synthesis_b,omitempty"`
}

// ThesisChange represents a change in a mode's thesis.
type ThesisChange struct {
	// ModeID is the mode that changed.
	ModeID string `json:"mode_id"`

	// ThesisA is the thesis in run A.
	ThesisA string `json:"thesis_a"`

	// ThesisB is the thesis in run B.
	ThesisB string `json:"thesis_b"`
}

// ContributionDiff tracks changes in contribution scores between runs.
type ContributionDiff struct {
	// ScoreDeltas maps mode IDs to their score changes.
	ScoreDeltas []ScoreDelta `json:"score_deltas,omitempty"`

	// RankChanges maps mode IDs to their rank changes.
	RankChanges []RankChange `json:"rank_changes,omitempty"`

	// OverlapRateA is the overlap rate in run A.
	OverlapRateA float64 `json:"overlap_rate_a"`

	// OverlapRateB is the overlap rate in run B.
	OverlapRateB float64 `json:"overlap_rate_b"`

	// DiversityScoreA is the diversity score in run A.
	DiversityScoreA float64 `json:"diversity_score_a"`

	// DiversityScoreB is the diversity score in run B.
	DiversityScoreB float64 `json:"diversity_score_b"`
}

// ScoreDelta represents a change in contribution score for a mode.
type ScoreDelta struct {
	// ModeID is the mode.
	ModeID string `json:"mode_id"`

	// ScoreA is the score in run A.
	ScoreA float64 `json:"score_a"`

	// ScoreB is the score in run B.
	ScoreB float64 `json:"score_b"`

	// Delta is ScoreB - ScoreA.
	Delta float64 `json:"delta"`
}

// RankChange represents a change in contribution rank for a mode.
type RankChange struct {
	// ModeID is the mode.
	ModeID string `json:"mode_id"`

	// RankA is the rank in run A (1 = highest).
	RankA int `json:"rank_a"`

	// RankB is the rank in run B.
	RankB int `json:"rank_b"`

	// Delta is RankB - RankA (negative = improved).
	Delta int `json:"delta"`
}

// CompareInput holds the data needed to compare two ensemble runs.
type CompareInput struct {
	// RunID is the identifier for this run.
	RunID string

	// ModeIDs lists the modes used in this run (sorted).
	ModeIDs []string

	// Outputs are the mode outputs.
	Outputs []ModeOutput

	// Provenance is the provenance tracker (optional).
	Provenance *ProvenanceTracker

	// Contributions is the contribution report (optional).
	Contributions *ContributionReport

	// SynthesisOutput is the final synthesis (optional).
	SynthesisOutput string
}

// Compare computes the deterministic diff between two ensemble runs.
func Compare(runA, runB CompareInput) *ComparisonResult {
	result := &ComparisonResult{
		RunA:        runA.RunID,
		RunB:        runB.RunID,
		GeneratedAt: time.Now(),
	}

	slog.Debug("comparing ensemble runs",
		"run_a", runA.RunID,
		"run_b", runB.RunID,
	)

	// Compare modes
	result.ModeDiff = compareModes(runA.ModeIDs, runB.ModeIDs)

	// Compare findings
	result.FindingsDiff = compareFindings(runA.Outputs, runB.Outputs)

	// Compare conclusions
	result.ConclusionDiff = compareConclusions(runA, runB)

	// Compare contributions
	result.ContributionDiff = compareContributions(runA.Contributions, runB.Contributions)

	// Generate summary
	result.Summary = generateComparisonSummary(result)

	return result
}

// compareModes computes the diff between two mode lists.
func compareModes(modesA, modesB []string) ModeDiff {
	setA := make(map[string]bool, len(modesA))
	for _, m := range modesA {
		setA[m] = true
	}

	setB := make(map[string]bool, len(modesB))
	for _, m := range modesB {
		setB[m] = true
	}

	diff := ModeDiff{}

	// Find added (in B, not in A)
	for _, m := range modesB {
		if !setA[m] {
			diff.Added = append(diff.Added, m)
		}
	}

	// Find removed (in A, not in B)
	for _, m := range modesA {
		if !setB[m] {
			diff.Removed = append(diff.Removed, m)
		}
	}

	// Find unchanged (in both)
	for _, m := range modesA {
		if setB[m] {
			diff.Unchanged = append(diff.Unchanged, m)
		}
	}

	// Sort for determinism
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Unchanged)

	diff.AddedCount = len(diff.Added)
	diff.RemovedCount = len(diff.Removed)
	diff.UnchangedCount = len(diff.Unchanged)

	return diff
}

// compareFindings computes the diff between findings using stable FindingIDs.
func compareFindings(outputsA, outputsB []ModeOutput) FindingsDiff {
	diff := FindingsDiff{}

	// Build finding maps using stable IDs
	findingsA := buildFindingMap(outputsA)
	findingsB := buildFindingMap(outputsB)

	// Find new (in B, not in A)
	for id, entry := range findingsB {
		if _, exists := findingsA[id]; !exists {
			diff.New = append(diff.New, entry)
		}
	}

	// Find missing (in A, not in B)
	for id, entry := range findingsA {
		if _, exists := findingsB[id]; !exists {
			diff.Missing = append(diff.Missing, entry)
		}
	}

	// Find changed and unchanged
	for id, entryA := range findingsA {
		if entryB, exists := findingsB[id]; exists {
			changes := compareFindingEntry(entryA, entryB)
			if len(changes) > 0 {
				diff.Changed = append(diff.Changed, FindingChange{
					FindingID:   id,
					ModeID:      entryA.ModeID,
					TextA:       entryA.Text,
					TextB:       entryB.Text,
					ImpactA:     entryA.Impact,
					ImpactB:     entryB.Impact,
					ConfidenceA: entryA.Confidence,
					ConfidenceB: entryB.Confidence,
					Changes:     changes,
				})
			} else {
				diff.Unchanged = append(diff.Unchanged, entryA)
			}
		}
	}

	// Sort for determinism
	sort.Slice(diff.New, func(i, j int) bool { return diff.New[i].FindingID < diff.New[j].FindingID })
	sort.Slice(diff.Missing, func(i, j int) bool { return diff.Missing[i].FindingID < diff.Missing[j].FindingID })
	sort.Slice(diff.Changed, func(i, j int) bool { return diff.Changed[i].FindingID < diff.Changed[j].FindingID })
	sort.Slice(diff.Unchanged, func(i, j int) bool { return diff.Unchanged[i].FindingID < diff.Unchanged[j].FindingID })

	diff.NewCount = len(diff.New)
	diff.MissingCount = len(diff.Missing)
	diff.ChangedCount = len(diff.Changed)
	diff.UnchangedCount = len(diff.Unchanged)

	return diff
}

// buildFindingMap creates a map from FindingID to FindingDiffEntry.
func buildFindingMap(outputs []ModeOutput) map[string]FindingDiffEntry {
	result := make(map[string]FindingDiffEntry)
	for _, output := range outputs {
		for _, finding := range output.TopFindings {
			id := GenerateFindingID(output.ModeID, finding.Finding)
			result[id] = FindingDiffEntry{
				FindingID:  id,
				ModeID:     output.ModeID,
				Text:       finding.Finding,
				Impact:     finding.Impact,
				Confidence: finding.Confidence,
			}
		}
	}
	return result
}

// compareFindingEntry compares two findings and returns a list of changes.
func compareFindingEntry(a, b FindingDiffEntry) []string {
	var changes []string

	if a.Text != b.Text {
		changes = append(changes, "text")
	}
	if a.Impact != b.Impact {
		changes = append(changes, fmt.Sprintf("impact: %s -> %s", a.Impact, b.Impact))
	}
	if a.Confidence != b.Confidence {
		changes = append(changes, fmt.Sprintf("confidence: %.2f -> %.2f", a.Confidence, b.Confidence))
	}

	return changes
}

// compareConclusions computes the diff between conclusions/theses.
func compareConclusions(runA, runB CompareInput) ConclusionDiff {
	diff := ConclusionDiff{}

	// Build thesis maps
	thesesA := make(map[string]string)
	for _, output := range runA.Outputs {
		thesesA[output.ModeID] = output.Thesis
	}

	thesesB := make(map[string]string)
	for _, output := range runB.Outputs {
		thesesB[output.ModeID] = output.Thesis
	}

	// Find thesis changes for modes in both runs
	allModes := make(map[string]bool)
	for m := range thesesA {
		allModes[m] = true
	}
	for m := range thesesB {
		allModes[m] = true
	}

	for modeID := range allModes {
		thesisA := thesesA[modeID]
		thesisB := thesesB[modeID]

		// Only include if thesis changed and mode was in both runs
		if _, inA := thesesA[modeID]; inA {
			if _, inB := thesesB[modeID]; inB {
				if thesisA != thesisB {
					diff.ThesisChanges = append(diff.ThesisChanges, ThesisChange{
						ModeID:  modeID,
						ThesisA: thesisA,
						ThesisB: thesisB,
					})
				}
			}
		}
	}

	// Sort for determinism
	sort.Slice(diff.ThesisChanges, func(i, j int) bool {
		return diff.ThesisChanges[i].ModeID < diff.ThesisChanges[j].ModeID
	})

	// Compare synthesis outputs
	diff.SynthesisChanged = runA.SynthesisOutput != runB.SynthesisOutput
	if diff.SynthesisChanged {
		diff.SynthesisA = truncateForDiff(runA.SynthesisOutput, 500)
		diff.SynthesisB = truncateForDiff(runB.SynthesisOutput, 500)
	}

	return diff
}

// compareContributions computes the diff between contribution reports.
func compareContributions(reportA, reportB *ContributionReport) ContributionDiff {
	diff := ContributionDiff{}

	if reportA == nil || reportB == nil {
		return diff
	}

	diff.OverlapRateA = reportA.OverlapRate
	diff.OverlapRateB = reportB.OverlapRate
	diff.DiversityScoreA = reportA.DiversityScore
	diff.DiversityScoreB = reportB.DiversityScore

	// Build score maps
	scoresA := make(map[string]ContributionScore)
	for _, s := range reportA.Scores {
		scoresA[s.ModeID] = s
	}

	scoresB := make(map[string]ContributionScore)
	for _, s := range reportB.Scores {
		scoresB[s.ModeID] = s
	}

	// Collect all modes
	allModes := make(map[string]bool)
	for m := range scoresA {
		allModes[m] = true
	}
	for m := range scoresB {
		allModes[m] = true
	}

	// Compute score deltas
	for modeID := range allModes {
		scoreA := scoresA[modeID]
		scoreB := scoresB[modeID]

		// Only compare modes in both runs
		if _, inA := scoresA[modeID]; inA {
			if _, inB := scoresB[modeID]; inB {
				delta := scoreB.Score - scoreA.Score
				if delta != 0 {
					diff.ScoreDeltas = append(diff.ScoreDeltas, ScoreDelta{
						ModeID: modeID,
						ScoreA: scoreA.Score,
						ScoreB: scoreB.Score,
						Delta:  delta,
					})
				}

				rankDelta := scoreB.Rank - scoreA.Rank
				if rankDelta != 0 {
					diff.RankChanges = append(diff.RankChanges, RankChange{
						ModeID: modeID,
						RankA:  scoreA.Rank,
						RankB:  scoreB.Rank,
						Delta:  rankDelta,
					})
				}
			}
		}
	}

	// Sort for determinism
	sort.Slice(diff.ScoreDeltas, func(i, j int) bool {
		return diff.ScoreDeltas[i].ModeID < diff.ScoreDeltas[j].ModeID
	})
	sort.Slice(diff.RankChanges, func(i, j int) bool {
		return diff.RankChanges[i].ModeID < diff.RankChanges[j].ModeID
	})

	return diff
}

// generateComparisonSummary creates a human-readable summary of the diff.
func generateComparisonSummary(result *ComparisonResult) string {
	var parts []string

	// Mode changes
	if result.ModeDiff.AddedCount > 0 {
		parts = append(parts, fmt.Sprintf("+%d modes", result.ModeDiff.AddedCount))
	}
	if result.ModeDiff.RemovedCount > 0 {
		parts = append(parts, fmt.Sprintf("-%d modes", result.ModeDiff.RemovedCount))
	}

	// Finding changes
	if result.FindingsDiff.NewCount > 0 {
		parts = append(parts, fmt.Sprintf("+%d findings", result.FindingsDiff.NewCount))
	}
	if result.FindingsDiff.MissingCount > 0 {
		parts = append(parts, fmt.Sprintf("-%d findings", result.FindingsDiff.MissingCount))
	}
	if result.FindingsDiff.ChangedCount > 0 {
		parts = append(parts, fmt.Sprintf("~%d findings modified", result.FindingsDiff.ChangedCount))
	}

	// Conclusion changes
	if len(result.ConclusionDiff.ThesisChanges) > 0 {
		parts = append(parts, fmt.Sprintf("%d thesis changes", len(result.ConclusionDiff.ThesisChanges)))
	}
	if result.ConclusionDiff.SynthesisChanged {
		parts = append(parts, "synthesis changed")
	}

	// Contribution changes
	if len(result.ContributionDiff.RankChanges) > 0 {
		parts = append(parts, fmt.Sprintf("%d rank changes", len(result.ContributionDiff.RankChanges)))
	}

	if len(parts) == 0 {
		return "No differences found"
	}

	return strings.Join(parts, ", ")
}

// truncateForDiff truncates text for display in diffs.
func truncateForDiff(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

// FormatComparison produces a human-readable comparison report.
func FormatComparison(result *ComparisonResult) string {
	if result == nil {
		return "No comparison result"
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Ensemble Comparison: %s vs %s\n", result.RunA, result.RunB)
	fmt.Fprintf(&b, "Generated: %s\n", result.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Summary: %s\n", result.Summary)
	b.WriteString("\n")

	// Mode diff
	b.WriteString("Mode Changes:\n")
	if result.ModeDiff.AddedCount > 0 {
		fmt.Fprintf(&b, "  Added (%d): %s\n", result.ModeDiff.AddedCount, strings.Join(result.ModeDiff.Added, ", "))
	}
	if result.ModeDiff.RemovedCount > 0 {
		fmt.Fprintf(&b, "  Removed (%d): %s\n", result.ModeDiff.RemovedCount, strings.Join(result.ModeDiff.Removed, ", "))
	}
	if result.ModeDiff.AddedCount == 0 && result.ModeDiff.RemovedCount == 0 {
		fmt.Fprintf(&b, "  No mode changes (%d unchanged)\n", result.ModeDiff.UnchangedCount)
	}
	b.WriteString("\n")

	// Findings diff
	b.WriteString("Finding Changes:\n")
	fmt.Fprintf(&b, "  New: %d | Missing: %d | Changed: %d | Unchanged: %d\n",
		result.FindingsDiff.NewCount,
		result.FindingsDiff.MissingCount,
		result.FindingsDiff.ChangedCount,
		result.FindingsDiff.UnchangedCount)

	if len(result.FindingsDiff.New) > 0 {
		b.WriteString("  New findings:\n")
		for i, f := range result.FindingsDiff.New {
			if i >= 5 {
				fmt.Fprintf(&b, "    ... and %d more\n", len(result.FindingsDiff.New)-5)
				break
			}
			fmt.Fprintf(&b, "    + [%s] %s\n", f.ModeID, truncateForDiff(f.Text, 60))
		}
	}

	if len(result.FindingsDiff.Missing) > 0 {
		b.WriteString("  Missing findings:\n")
		for i, f := range result.FindingsDiff.Missing {
			if i >= 5 {
				fmt.Fprintf(&b, "    ... and %d more\n", len(result.FindingsDiff.Missing)-5)
				break
			}
			fmt.Fprintf(&b, "    - [%s] %s\n", f.ModeID, truncateForDiff(f.Text, 60))
		}
	}

	if len(result.FindingsDiff.Changed) > 0 {
		b.WriteString("  Changed findings:\n")
		for i, c := range result.FindingsDiff.Changed {
			if i >= 3 {
				fmt.Fprintf(&b, "    ... and %d more\n", len(result.FindingsDiff.Changed)-3)
				break
			}
			fmt.Fprintf(&b, "    ~ [%s] %s\n", c.ModeID, strings.Join(c.Changes, ", "))
		}
	}
	b.WriteString("\n")

	// Contribution diff
	if len(result.ContributionDiff.RankChanges) > 0 {
		b.WriteString("Contribution Rank Changes:\n")
		for _, rc := range result.ContributionDiff.RankChanges {
			direction := "↓"
			if rc.Delta < 0 {
				direction = "↑"
			}
			fmt.Fprintf(&b, "  %s: #%d → #%d (%s%d)\n", rc.ModeID, rc.RankA, rc.RankB, direction, absInt(rc.Delta))
		}
		b.WriteString("\n")
	}

	// Synthesis diff
	if result.ConclusionDiff.SynthesisChanged {
		b.WriteString("Synthesis Output: Changed\n")
	}

	return b.String()
}

// absInt returns the absolute value of an integer.
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ComparisonResultJSON returns the comparison result as JSON.
