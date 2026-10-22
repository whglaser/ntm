package pipeline

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"os"
	"slices"
	"strings"
	"time"
)

// DefaultOutputStabilityWindow is how long every declared step output must be
// byte-identical before the artifact fallback completes a step. Long enough
// that an agent still streaming or rewriting a report cannot trip it, short
// enough that a finished step does not sit out the rest of its timeout.
const DefaultOutputStabilityWindow = 15 * time.Second

// waitIdleOptions carries the per-wait completion policy for waitForIdle.
// The zero value reproduces the original behavior: detector idle readings
// only, with the package default idle streak.
type waitIdleOptions struct {
	// StablePolls is how many consecutive idle readings complete the wait.
	// Zero uses idleStablePolls (3).
	StablePolls int
	// Artifacts is the declared-output fallback. Nil disables it; steps with
	// no usable declared outputs (empty paths, unsubstitutable variables) get
	// a nil watcher rather than an empty one.
	Artifacts *artifactStability
}

// artifactStability tracks whether every declared step output exists and has
// been byte-identical for a full stability window. It is the completion
// fallback for long steps whose agent finished writing its deliverable but
// whose pane never reads idle (the answers-style stall): reading the files
// is independent of pane state, so a stuck classification cannot block it.
//
// A missing or unreadable path resets the window. A path that never appears
// can therefore never complete the wait — the fallback only ever confirms
// artifacts that are actually on disk.
type artifactStability struct {
	paths  []string
	window time.Duration

	digest     [sha256.Size]byte
	haveDigest bool
	since      time.Time
}

// newArtifactStability returns a watcher over paths, or nil when there is
// nothing to watch. Duplicate paths collapse (order-preserving) so a doubled
// declaration cannot hash the same file twice. A negative window is treated
// as zero: the second confirming sample completes.
func newArtifactStability(paths []string, window time.Duration) *artifactStability {
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || slices.Contains(unique, path) {
			continue
		}
		unique = append(unique, path)
	}
	if len(unique) == 0 {
		return nil
	}
	if window < 0 {
		window = 0
	}
	return &artifactStability{paths: unique, window: window}
}

// satisfiedAt reports whether every watched path exists and the set has been
// byte-identical since at least now-window. The first observation only
// records the baseline (returns false); the wait must see the same digest
// again after the window passes.
func (a *artifactStability) satisfiedAt(now time.Time) bool {
	if a == nil || len(a.paths) == 0 {
		return false
	}
	digest, ok := digestArtifacts(a.paths)
	if !ok {
		a.haveDigest = false
		a.since = time.Time{}
		return false
	}
	if !a.haveDigest || digest != a.digest {
		a.digest = digest
		a.haveDigest = true
		a.since = now
		return false
	}
	return !a.since.IsZero() && now.Sub(a.since) >= a.window
}

// watchedPaths returns the resolved paths for logging. The slice is a copy;
// callers cannot mutate the watcher's state.
func (a *artifactStability) watchedPaths() []string {
	if a == nil {
		return nil
	}
	return slices.Clone(a.paths)
}

// waitIdleOptionsForStep builds the completion policy for one agent step: the
// configured idle streak plus the declared-output fallback. Each declared
// path is variable-substituted exactly like Workflow.Outputs, so `${vars.*}`
// paths resolve the same way in both places.
func (e *Executor) waitIdleOptionsForStep(step *Step) waitIdleOptions {
	opts := waitIdleOptions{StablePolls: e.config.IdleStablePolls}
	if step == nil {
		return opts
	}
	paths := make([]string, 0, len(step.Outputs))
	for _, decl := range step.Outputs {
		if strings.TrimSpace(decl.Path) == "" {
			continue
		}
		resolved := strings.TrimSpace(e.substituteVariables(decl.Path))
		if resolved == "" {
			continue
		}
		paths = append(paths, resolved)
	}
	opts.Artifacts = newArtifactStability(paths, e.outputStabilityWindow())
	return opts
}

// outputStabilityWindow returns the configured artifact-stability window,
// defaulting to DefaultOutputStabilityWindow when unset.
func (e *Executor) outputStabilityWindow() time.Duration {
	if e.config.OutputStabilityWindow > 0 {
		return e.config.OutputStabilityWindow
	}
	return DefaultOutputStabilityWindow
}

// digestArtifacts hashes the full content of every path, length-prefixed so
// two files cannot collide across a boundary. It reports ok=false when any
// path is missing or unreadable; an empty file is a valid, stable artifact.
func digestArtifacts(paths []string) ([sha256.Size]byte, bool) {
	hasher := sha256.New()
	var digest [sha256.Size]byte
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return digest, false
		}
		writeHashedField(hasher, []byte(path))
		writeHashedField(hasher, data)
	}
	copy(digest[:], hasher.Sum(nil))
	return digest, true
}

// writeHashedField feeds a little-endian length followed by the bytes, so a
// path/content boundary cannot be confused with content.
func writeHashedField(hasher hash.Hash, data []byte) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(data)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(data)
}
