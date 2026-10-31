package status

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The wrapped payload below reconstructs the live azure-foundry rejection
// (SDR run-20261030-021443-86ee): the pane width split the JSON mid-token, so
// a line-based scan would miss `content_filter` entirely. Detection must see
// through terminal wrapping without rewriting the frame.
const providerFilterPayload = `Error: 400 data: {"created":1793326658,"id":"chatcmpl-cac10c2a880042c485383235011a3","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"content_filter","index":0,"content_filter_results":{"error":{"code":"content_filter","message":"Response content blocked by label 'Jailbreak'."}}}]}`

// wrapEvery inserts terminal-style hard wraps so tokens are split without any
// word boundary, exactly like the live capture.
func wrapEvery(s string, width int) string {
	var parts []string
	for len(s) > width {
		parts = append(parts, s[:width])
		s = s[width:]
	}
	parts = append(parts, s)
	return strings.Join(parts, "\n")
}

func TestDetectProviderRejectionContentFilterWrapped(t *testing.T) {
	frame := wrapEvery(providerFilterPayload, 25) + "\nIf this looks like a pi bug, /bug sends a report to the developers."

	rejection, ok := DetectProviderRejection(frame)
	if !ok {
		t.Fatalf("wrapped content-filter frame was not recognized: %q", frame)
	}
	if rejection.Code != "content_filter" {
		t.Fatalf("code = %q, want content_filter", rejection.Code)
	}
	if rejection.Label != "Jailbreak" {
		t.Fatalf("label = %q, want Jailbreak", rejection.Label)
	}
	if rejection.HTTPStatus != 400 {
		t.Fatalf("http status = %d, want 400", rejection.HTTPStatus)
	}
	if !strings.Contains(rejection.Message, "Response content blocked") {
		t.Fatalf("message = %q, want the provider's blocked-response message", rejection.Message)
	}
	if got, want := rejection.Error(), "provider rejected the response: content_filter (Jailbreak)"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestDetectProviderRejectionUnwrappedAndStyled(t *testing.T) {
	styled := "\x1b[31m" + providerFilterPayload + "\x1b[0m"
	rejection, ok := DetectProviderRejection(styled)
	if !ok || rejection.Code != "content_filter" || rejection.Label != "Jailbreak" {
		t.Fatalf("styled unwrapped frame not recognized: ok=%v rejection=%+v", ok, rejection)
	}
}

func TestDetectProviderRejectionFinishReasonOnly(t *testing.T) {
	frame := `stream error: {"choices":[{"finish_reason":"content_filter"}]}`
	rejection, ok := DetectProviderRejection(frame)
	if !ok {
		t.Fatal("finish_reason content_filter was not recognized")
	}
	if rejection.Code != "content_filter" {
		t.Fatalf("code = %q, want the content_filter fallback", rejection.Code)
	}
}

func TestDetectProviderRejectionResultsKeyOnly(t *testing.T) {
	frame := `Error: 400 data: {"choices":[{"content_filter_results":{}}]}`
	if _, ok := DetectProviderRejection(frame); !ok {
		t.Fatal("content_filter_results payload was not recognized")
	}
}

func TestDetectProviderRejectionGenericProviderError(t *testing.T) {
	frame := `Error: 401 data: {"error":{"code":"invalid_api_key","message":"Incorrect API key provided"}}`
	rejection, ok := DetectProviderRejection(frame)
	if !ok {
		t.Fatal("generic provider 4xx error was not recognized")
	}
	if rejection.Code != "invalid_api_key" || rejection.HTTPStatus != 401 {
		t.Fatalf("rejection = %+v, want invalid_api_key/401", rejection)
	}
	if got, want := rejection.Error(), "provider rejected the response: HTTP 401: invalid_api_key"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestDetectProviderRejectionIgnoresProseAndNormalFrames(t *testing.T) {
	for name, frame := range map[string]string{
		"prose about the filter": "The review notes that content_filter_results can be noisy, and content filter policy is deployment-specific.",
		"normal answer":          "Pi v1.3.0\n\nFindings written to reviews/active/foo.md\n\nDone.",
		"http status in prose":   "The upstream API returned 400 for that test payload, but this run used the local fixture.",
		"bare error line":        "Error: 404 not found in the logs",
	} {
		if rejection, ok := DetectProviderRejection(frame); ok {
			t.Fatalf("%s: false positive %+v for %q", name, rejection, frame)
		}
	}
}

func TestProviderRejectionInWalksWrappedErrors(t *testing.T) {
	_, ok := ProviderRejectionIn(nil)
	if ok {
		t.Fatal("nil error must not be a provider rejection")
	}

	rejection := ProviderRejection{Code: "content_filter", Label: "Jailbreak"}
	wrapped := fmt.Errorf("waiting for the original agent delivery: %w", fmt.Errorf("poll: %w", rejection))
	got, ok := ProviderRejectionIn(wrapped)
	if !ok || got.Code != "content_filter" || got.Label != "Jailbreak" {
		t.Fatalf("ProviderRejectionIn = %+v ok=%v", got, ok)
	}
	if _, ok := ProviderRejectionIn(errors.New("saved agent output boundary is no longer visible")); ok {
		t.Fatal("boundary loss was misread as a provider rejection")
	}
}

func TestSanitizeProviderText(t *testing.T) {
	got := sanitizeProviderText("  line\none\x00\ttwo   three  ", 10)
	if got != "line one t" {
		t.Fatalf("sanitized = %q, want %q", got, "line one t")
	}
	if got := sanitizeProviderText("plain", 64); got != "plain" {
		t.Fatalf("short text changed: %q", got)
	}
}
