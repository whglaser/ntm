package robot

// recordWaitTransitions establishes each pane's baseline at its first successful
// observation. An unreadable pane on the first poll must not bypass the requested
// leave-and-return cycle when it first becomes readable in the target state.
func recordWaitTransitions(
	activities []*AgentActivity,
	conditions []string,
	initiallyInTarget map[string]bool,
	sawTransition map[string]bool,
) {
	for _, activity := range activities {
		inTarget := meetsAllWaitConditions(activity, conditions)
		if _, observed := initiallyInTarget[activity.PaneID]; !observed {
			initiallyInTarget[activity.PaneID] = inTarget
		} else if initiallyInTarget[activity.PaneID] && !inTarget {
			sawTransition[activity.PaneID] = true
		}
	}
}

// checkWaitPaneObservations keeps failed captures in the selected target set.
// ANY/count waits can still succeed on enough positively observed matches, but
// ALL waits cannot. ExitOnError also requires observing every selected pane:
// unreadable output cannot establish the absence of an agent error.
func checkWaitPaneObservations(
	activities []*AgentActivity,
	unobservedPanes []string,
	opts WaitOptions,
	conditions []string,
	initiallyInTarget map[string]bool,
	sawTransition map[string]bool,
) (bool, []WaitAgentInfo, []string) {
	met := len(conditions) == 0
	var matching []WaitAgentInfo
	var pending []string
	if len(conditions) > 0 {
		met, matching, pending = checkWaitConditionMetWithTransition(
			activities, opts, conditions, initiallyInTarget, sawTransition,
		)
	}

	if len(unobservedPanes) > 0 && (len(conditions) > 0 || opts.ExitOnError) {
		pending = append(pending, unobservedPanes...)
		if !opts.WaitForAny || opts.ExitOnError {
			met = false
		}
	}
	return met, matching, pending
}

// waitAgentEvidence copies the classifier evidence an operator needs to
// understand why a pane matched or stayed pending (ntm-l6cuz.7).
func waitAgentEvidence(activity *AgentActivity) WaitAgentInfo {
	if activity == nil {
		return WaitAgentInfo{}
	}
	info := WaitAgentInfo{
		Pane: activity.PaneID, State: string(activity.State),
		AgentType: activity.AgentType, StateSource: activity.StateSource,
	}
	if !activity.StateSince.IsZero() {
		info.StateSince = FormatTimestamp(activity.StateSince)
	}
	if len(activity.DetectedPatterns) > 0 {
		info.DetectedPatterns = append([]string(nil), activity.DetectedPatterns...)
	}
	return info
}

// pendingWaitEvidence describes the panes still pending on a poll, including
// unobserved panes that could not be classified this round.
func pendingWaitEvidence(activities []*AgentActivity, unobserved, pending []string) []WaitAgentInfo {
	if len(pending) == 0 {
		return nil
	}
	remaining := make(map[string]struct{}, len(pending))
	for _, paneID := range pending {
		remaining[paneID] = struct{}{}
	}
	evidence := make([]WaitAgentInfo, 0, len(pending))
	for _, activity := range activities {
		if activity == nil {
			continue
		}
		if _, ok := remaining[activity.PaneID]; !ok {
			continue
		}
		delete(remaining, activity.PaneID)
		evidence = append(evidence, waitAgentEvidence(activity))
	}
	for _, paneID := range unobserved {
		if _, ok := remaining[paneID]; !ok {
			continue
		}
		delete(remaining, paneID)
		evidence = append(evidence, WaitAgentInfo{Pane: paneID, State: "unknown"})
	}
	return evidence
}
