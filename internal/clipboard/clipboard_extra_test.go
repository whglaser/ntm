package clipboard

import (
	"testing"
)

// =============================================================================
// chooseBackend: additional branch coverage
// =============================================================================

func TestChooseBackendDarwinNoPbcopy(t *testing.T) {
	t.Parallel()
	det := newStubDetector("darwin", nil, nil, "")
	_, err := chooseBackend(det)
	if err == nil {
		t.Fatal("expected error when pbcopy missing on darwin")
	}
}

func TestChooseBackendUnsupportedOS(t *testing.T) {
	t.Parallel()
	det := newStubDetector("freebsd", nil, nil, "")
	_, err := chooseBackend(det)
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

func TestChooseBackendLinuxWlCopyLastResort(t *testing.T) {
	t.Parallel()
	// No wayland env, no DISPLAY, no WSL, but wl-copy available as last resort
	det := newStubDetector("linux", nil, map[string]bool{"wl-copy": true}, "")
	b, err := chooseBackend(det)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.name() != "wl-copy" {
		t.Errorf("expected wl-copy backend, got %s", b.name())
	}
}

func TestChooseBackendLinuxClipExeFallback(t *testing.T) {
	t.Parallel()
	// No wayland, no DISPLAY, no WSL, but clip.exe available
	det := newStubDetector("linux", nil, map[string]bool{"clip.exe": true}, "")
	b, err := chooseBackend(det)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.name() != "wsl-clipboard" {
		t.Errorf("expected wsl-clipboard backend, got %s", b.name())
	}
}

func TestChooseBackendWSLNoPowershell(t *testing.T) {
	t.Parallel()
	det := newStubDetector("linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
		map[string]bool{"clip.exe": true}, "")
	b, err := chooseBackend(det)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := b.(*wslBackend); !ok {
		t.Fatalf("expected *wslBackend, got %T", b)
	}
}

func TestChooseBackendWSLNoClipExeFallthrough(t *testing.T) {
	t.Parallel()
	// WSL env but no clip.exe - should fall through to other Linux options
	det := newStubDetector("linux",
		map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "DISPLAY": ":0"},
		map[string]bool{"xclip": true}, "")
	b, err := chooseBackend(det)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.name() != "xclip" {
		t.Errorf("expected xclip fallback, got %s", b.name())
	}
}

func TestChooseBackendWaylandDisplay(t *testing.T) {
	t.Parallel()
	// Wayland detected via WAYLAND_DISPLAY env instead of XDG_SESSION_TYPE
	det := newStubDetector("linux",
		map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
		map[string]bool{"wl-copy": true, "wl-paste": true}, "")
	b, err := chooseBackend(det)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.name() != "wl-copy" {
		t.Errorf("expected wl-copy backend, got %s", b.name())
	}
}

func TestChooseBackendWSLProcVersion(t *testing.T) {
	t.Parallel()
	// WSL detected via /proc/version instead of env
	det := newStubDetector("linux", nil,
		map[string]bool{"clip.exe": true, "powershell.exe": true},
		"Linux 5.15.0-microsoft-standard-WSL2")
	b, err := chooseBackend(det)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.name() != "wsl-clipboard" {
		t.Errorf("expected wsl-clipboard backend, got %s", b.name())
	}
}

// =============================================================================
// newWithDetector + clipboardImpl proxy methods
// =============================================================================

func TestNewWithDetector(t *testing.T) {
	t.Parallel()

	t.Run("success returns clipboard", func(t *testing.T) {
		t.Parallel()
		det := newStubDetector("linux",
			map[string]string{"XDG_SESSION_TYPE": "wayland"},
			map[string]bool{"wl-copy": true, "wl-paste": true}, "")
		cb, err := newWithDetector(det)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cb == nil {
			t.Fatal("expected non-nil clipboard")
		}
		if cb.Backend() != "wl-copy" {
			t.Errorf("Backend() = %q, want wl-copy", cb.Backend())
		}
		if !cb.Available() {
			t.Error("Available() should return true")
		}
	})

	t.Run("error when no backend", func(t *testing.T) {
		t.Parallel()
		det := newStubDetector("linux", nil, nil, "")
		cb, err := newWithDetector(det)
		if err == nil {
			t.Fatal("expected error when no clipboard backend found")
		}
		if cb != nil {
			t.Error("expected nil clipboard on error")
		}
	})
}

// =============================================================================
// Backend name and available methods
// =============================================================================

func TestBackendNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		b    backend
		want string
	}{
		{"pbcopy", pbcopyBackend{}, "pbcopy"},
		{"wl-copy", wlBackend{}, "wl-copy"},
		{"xclip", xclipBackend{}, "xclip"},
		{"xsel", xselBackend{}, "xsel"},
		{"wsl", wslBackend{}, "wsl-clipboard"},
		{"tmux", tmuxBackend{}, "tmux-buffer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.b.name(); got != tc.want {
				t.Errorf("name() = %q, want %q", got, tc.want)
			}
			if !tc.b.available() {
				t.Errorf("available() = false, want true")
			}
		})
	}
}
