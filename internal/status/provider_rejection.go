package status

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Provider rejection recognition. pi renders a provider/API rejection as a
// terminal error frame ("Error: 400 data: {...}"), for example an
// azure-foundry content-filter refusal. The delivery wait must classify that
// frame as the provider's verdict instead of reporting the scrolled-away
// pre-send boundary as the failure, and it must never attribute the error
// frame as a completed answer (ntm-l6cuz.10). Recognition is read-only
// classification: nothing is fabricated, truncated or rewritten.

// Provider metadata is bounded and value-safe. Detection runs over the
// de-wrapped frame because the pane geometry wraps the JSON mid-token (the
// live rejection split `"finish_reason":"con` / `tent_filter"`), and the
// operator-facing string carries only the provider's code and filter label,
// never the raw payload, which may quote prompt content.
const (
	providerCodeLimit    = 64
	providerMessageLimit = 200
	providerLabelLimit   = 64
)

var (
	reProviderHTTPError = regexp.MustCompile(`(?i)\bError:\s*(\d{3})\s+data:`)
	reJSONCode          = regexp.MustCompile(`"code"\s*:\s*"([^"\\]{1,64})"`)
	reJSONMessage       = regexp.MustCompile(`"message"\s*:\s*"([^"\\]{1,300})"`)
	reJSONFinishReason  = regexp.MustCompile(`"finish_reason"\s*:\s*"([^"\\]{1,64})"`)
	reBlockedResponse   = regexp.MustCompile(`(?i)Response content blocked(?: by label '([^']{1,64})')?`)
	reQuotedLabel       = regexp.MustCompile(`(?i)label\s+'([^']{1,64})'`)
)

// ProviderRejection is the classifiable, value-safe metadata of a provider/API
// error visible in an agent pane. It implements error so the delivery wait can
// carry the verdict through collect/waitForIdle as a distinct failure instead
// of the boundary-loss wording.
type ProviderRejection struct {
	HTTPStatus int
	Code       string
	Label      string
	Message    string
}

// Error renders the operator-facing reason. It intentionally carries only the
// provider's code and filter label; the raw payload stays in the step's pane
// output where it can be inspected but cannot leak into summaries.
func (r ProviderRejection) Error() string {
	detail := r.Code
	switch {
	case detail == "" && r.HTTPStatus != 0:
		detail = fmt.Sprintf("HTTP %d", r.HTTPStatus)
	case detail == "":
		detail = "unknown provider error"
	case r.HTTPStatus != 0 && r.Code != "content_filter":
		detail = fmt.Sprintf("HTTP %d: %s", r.HTTPStatus, r.Code)
	}
	if r.Label != "" {
		detail += " (" + r.Label + ")"
	}
	return "provider rejected the response: " + detail
}

// ProviderRejectionIn extracts a recognized provider rejection from an error
// chain. It is the wait-side half of DetectProviderRejection: callers that
// received an error from a delivery poll can ask whether the provider ended
// the turn.
func ProviderRejectionIn(err error) (ProviderRejection, bool) {
	if err == nil {
		return ProviderRejection{}, false
	}
	var rejection ProviderRejection
	if errors.As(err, &rejection) {
		return rejection, true
	}
	return ProviderRejection{}, false
}

// DetectProviderRejection reports whether a captured agent frame contains a
// terminal provider/API rejection and returns its metadata.
//
// A content-filter verdict is recognized from the provider payload itself
// (a quoted "code"/"finish_reason"/"content_filter_results" value, or the
// "Response content blocked" message). Other provider 4xx rejections are
// recognized from pi's rendered "Error: <4xx> data: {...}" frame. Prose that
// merely mentions content filtering is not a rejection: bare words never
// match, only the quoted provider fields or the rendered error frame do.
func DetectProviderRejection(frame string) (ProviderRejection, bool) {
	text := dewrapProviderFrame(frame)
	if strings.TrimSpace(text) == "" {
		return ProviderRejection{}, false
	}

	rejection := ProviderRejection{}
	if m := reProviderHTTPError.FindStringSubmatch(text); m != nil {
		rejection.HTTPStatus, _ = strconv.Atoi(m[1])
	}
	if code := firstProviderMatch(reJSONCode, text); code != "" {
		rejection.Code = sanitizeProviderText(code, providerCodeLimit)
	}
	if message := firstProviderMatch(reJSONMessage, text); message != "" {
		rejection.Message = sanitizeProviderText(message, providerMessageLimit)
	}
	if label := firstProviderMatch(reQuotedLabel, text); label != "" {
		rejection.Label = sanitizeProviderText(label, providerLabelLimit)
	}
	finish := firstProviderMatch(reJSONFinishReason, text)

	blocked := false
	if m := reBlockedResponse.FindStringSubmatch(text); m != nil {
		blocked = true
		if rejection.Message == "" {
			rejection.Message = sanitizeProviderText(m[0], providerMessageLimit)
		}
		if rejection.Label == "" && len(m) > 1 {
			rejection.Label = sanitizeProviderText(m[1], providerLabelLimit)
		}
	}

	contentFilter := strings.EqualFold(rejection.Code, "content_filter") ||
		strings.EqualFold(finish, "content_filter") ||
		strings.Contains(text, `"content_filter_results"`) ||
		(blocked && strings.Contains(text, `"error"`))
	if contentFilter {
		if rejection.Code == "" {
			rejection.Code = "content_filter"
		}
		return rejection, true
	}

	// A generic provider 4xx payload is equally terminal: the provider
	// rejected the turn, so waiting or retrying the unchanged prompt cannot
	// complete it. Require pi's rendered error prefix plus a structured error
	// payload so ordinary output that mentions HTTP status codes is not
	// misclassified.
	if rejection.HTTPStatus >= 400 && rejection.HTTPStatus < 500 {
		if rejection.Code != "" || rejection.Message != "" || strings.Contains(text, `"error"`) {
			if rejection.Code == "" {
				rejection.Code = fmt.Sprintf("http_%d", rejection.HTTPStatus)
			}
			return rejection, true
		}
	}
	return ProviderRejection{}, false
}

// dewrapProviderFrame strips ANSI styling and joins terminal-wrapped lines.
// Signatures are long and structurally quoted, so re-joining lines cannot
// create one by accident; it only restores tokens the pane width split.
func dewrapProviderFrame(frame string) string {
	plain := StripANSI(frame)
	if !strings.Contains(plain, "\n") {
		return plain
	}
	lines := strings.Split(plain, "\n")
	var b strings.Builder
	b.Grow(len(plain))
	for _, line := range lines {
		b.WriteString(line)
	}
	return b.String()
}

func firstProviderMatch(re *regexp.Regexp, text string) string {
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// sanitizeProviderText collapses whitespace, drops control characters, and
// bounds the result so an arbitrary provider payload cannot bloat or corrupt
// operator-facing text.
func sanitizeProviderText(s string, limit int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	var b strings.Builder
	b.Grow(len(collapsed))
	for _, r := range collapsed {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if runes := []rune(out); len(runes) > limit {
		out = strings.TrimSpace(string(runes[:limit]))
	}
	return out
}
