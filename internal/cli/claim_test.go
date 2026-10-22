package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateSoftClaimAndCheck(t *testing.T) {
	projectDir := t.TempDir()
	now := time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC)
	previousNow := softClaimNow
	softClaimNow = func() time.Time { return now }
	t.Cleanup(func() { softClaimNow = previousNow })

	claim, err := createSoftClaim(projectDir, "ntm-claim", "TopazMill", time.Minute)
	if err != nil {
		t.Fatalf("create soft claim: %v", err)
	}
	if claim.Agent != "TopazMill" || claim.BeadID != "ntm-claim" {
		t.Fatalf("claim = %#v", claim)
	}
	if want := filepath.Join(projectDir, ".ntm", "claims", "ntm-claim.json"); softClaimPath(projectDir, claim.BeadID) != want {
		t.Fatalf("claim path = %q, want %q", softClaimPath(projectDir, claim.BeadID), want)
	}

	status, err := checkSoftClaim(projectDir, "ntm-claim")
	if err != nil {
		t.Fatalf("check soft claim: %v", err)
	}
	if status.State != "claimed" || status.Claim == nil || status.Claim.Agent != "TopazMill" {
		t.Fatalf("status = %#v", status)
	}
}

func TestCreateSoftClaimRejectsActiveConflict(t *testing.T) {
	projectDir := t.TempDir()
	if _, err := createSoftClaim(projectDir, "ntm-conflict", "TopazMill", time.Minute); err != nil {
		t.Fatalf("create first soft claim: %v", err)
	}
	if _, err := createSoftClaim(projectDir, "ntm-conflict", "JadePond", time.Minute); err == nil {
		t.Fatal("create conflicting soft claim succeeded")
	}
}

func TestExpiredSoftClaimIsArchivedBeforeReclaim(t *testing.T) {
	projectDir := t.TempDir()
	now := time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC)
	previousNow := softClaimNow
	softClaimNow = func() time.Time { return now }
	t.Cleanup(func() { softClaimNow = previousNow })

	if _, err := createSoftClaim(projectDir, "ntm-expired", "TopazMill", time.Minute); err != nil {
		t.Fatalf("create first soft claim: %v", err)
	}
	now = now.Add(time.Minute)
	status, err := checkSoftClaim(projectDir, "ntm-expired")
	if err != nil {
		t.Fatalf("check expired claim: %v", err)
	}
	if status.State != "expired" {
		t.Fatalf("state = %q, want expired", status.State)
	}
	claim, err := createSoftClaim(projectDir, "ntm-expired", "JadePond", time.Minute)
	if err != nil {
		t.Fatalf("reclaim expired soft claim: %v", err)
	}
	if claim.Agent != "JadePond" {
		t.Fatalf("reclaimed agent = %q", claim.Agent)
	}
	expired, err := os.ReadDir(filepath.Join(projectDir, ".ntm", "claims", "expired"))
	if err != nil {
		t.Fatalf("read archived claims: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("archived claims = %d, want 1", len(expired))
	}
}

func TestListSoftClaimsExcludesExpiredAndKeepsEmptySlice(t *testing.T) {
	projectDir := t.TempDir()
	now := time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC)
	previousNow := softClaimNow
	softClaimNow = func() time.Time { return now }
	t.Cleanup(func() { softClaimNow = previousNow })

	claims, err := listSoftClaims(projectDir)
	if err != nil {
		t.Fatalf("list absent claims: %v", err)
	}
	if claims == nil || len(claims) != 0 {
		t.Fatalf("claims = %#v, want empty non-nil slice", claims)
	}
	if _, err := createSoftClaim(projectDir, "ntm-active", "TopazMill", time.Minute); err != nil {
		t.Fatalf("create active soft claim: %v", err)
	}
	if _, err := createSoftClaim(projectDir, "ntm-stale", "JadePond", time.Minute); err != nil {
		t.Fatalf("create stale soft claim: %v", err)
	}
	now = now.Add(time.Minute)
	claims, err = listSoftClaims(projectDir)
	if err != nil {
		t.Fatalf("list expired claims: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("claims = %#v, want no active claims", claims)
	}
	if _, err := createSoftClaim(projectDir, "ntm-fresh", "JadePond", time.Minute); err != nil {
		t.Fatalf("create fresh soft claim: %v", err)
	}
	claims, err = listSoftClaims(projectDir)
	if err != nil {
		t.Fatalf("list fresh claims: %v", err)
	}
	if len(claims) != 1 || claims[0].BeadID != "ntm-fresh" {
		t.Fatalf("claims = %#v, want ntm-fresh", claims)
	}
}

func TestClaimCommandIncludesHelperCommands(t *testing.T) {
	cmd := newClaimCmd()
	for _, name := range []string{"check", "list"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil || found == cmd {
			t.Fatalf("claim helper %q not registered: command=%v err=%v", name, found, err)
		}
	}
}

func TestSoftClaimRejectsUnsafeBeadIDAndTTL(t *testing.T) {
	projectDir := t.TempDir()
	if _, err := createSoftClaim(projectDir, "../escape", "TopazMill", time.Minute); err == nil {
		t.Fatal("unsafe bead ID succeeded")
	}
	if _, err := createSoftClaim(projectDir, "ntm-ttl", "TopazMill", 0); err == nil {
		t.Fatal("zero TTL succeeded")
	}
}

// Sub-issue IDs are the normal form for child beads (ntm-l6cuz.2) and the
// serve-side bead pattern accepts them; the soft-claim validator must too,
// without letting the ID escape the claim directory as a path component.
func TestSoftClaimAcceptsSubIssueIDsAndRejectsTraversal(t *testing.T) {
	projectDir := t.TempDir()
	for _, id := range []string{"ntm-l6cuz.2", "bd-2euwg.1", "ntm-32yad"} {
		claim, err := createSoftClaim(projectDir, id, "TestAgent", time.Minute)
		if err != nil {
			t.Fatalf("createSoftClaim(%q) = %v", id, err)
		}
		if claim.BeadID != id {
			t.Fatalf("claim bead ID = %q, want %q", claim.BeadID, id)
		}
	}
	for _, id := range []string{"..", ".", ".hidden", "ntm..l6cuz", "ntm-l6cuz.", "a/../b"} {
		if _, err := createSoftClaim(projectDir, id, "TestAgent", time.Minute); err == nil {
			t.Fatalf("unsafe bead ID %q succeeded", id)
		}
	}
}

func TestReadSoftClaimRejectsMalformedFile(t *testing.T) {
	projectDir := t.TempDir()
	claimPath := softClaimPath(projectDir, "ntm-invalid")
	if err := os.MkdirAll(filepath.Dir(claimPath), 0o755); err != nil {
		t.Fatalf("create claim directory: %v", err)
	}
	if err := os.WriteFile(claimPath, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write malformed claim: %v", err)
	}
	_, err := checkSoftClaim(projectDir, "ntm-invalid")
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check malformed claim error = %v", err)
	}
}
