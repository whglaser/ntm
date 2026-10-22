package programstatus

import (
	"encoding/base64"
	"strings"
	"time"
)

// Protocol bounds. pi caps a decoded message at 2048 bytes (its encoded form
// stays under 2732 bytes); reports larger than these are malformed and are
// discarded rather than buffered without bound.
const (
	maxBodyBytes    = 4096
	maxNumberDigits = 8
)

const (
	oscIntroducer   = 0x1b // ESC
	bel             = 0x07
	oscNumber       = "7501"
	queryPrefix     = "?"
	byteBackslash   = '\\'
	byteLeftBracket = ']'
	byteSemicolon   = ';'
	byteEquals      = '='
	byteColon       = ':'
	byteESC         = 0x1b
)

type parserState int

const (
	stateGround parserState = iota
	stateESC
	stateNumber
	stateIgnore
	stateBody
	stateBodyESC
	stateIgnoreESC
)

// Parser incrementally decodes OSC 7501 reports from a raw terminal byte
// stream. It is safe to reuse across the whole life of a pane tap: Feed may
// be called with arbitrary chunk boundaries, including splits inside a
// sequence introducer, the numeric parameter, a key/value pair, or between
// ESC and its ST backslash. No state is shared between Parser instances.
type Parser struct {
	paneID string
	now    func() time.Time

	state parserState
	// num accumulates the OSC numeric parameter; body accumulates the
	// payload of a 7501 sequence.
	num  []byte
	body []byte
}

// NewParser returns a parser that attributes decoded events to paneID.
// paneID may be empty when the caller attributes events itself.
func NewParser(paneID string) *Parser {
	return &Parser{
		paneID: paneID,
		now:    time.Now,
	}
}

// SetClock overrides the timestamp source for decoded events. Tests use it to
// make ObservedAt deterministic; production callers leave the default.
func (p *Parser) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	p.now = now
}

// Reset drops any partially parsed sequence. A pane tap calls it when the
// pane's process lifetime changes so bytes from a dead process cannot
// complete a sequence started by its successor.
func (p *Parser) Reset() {
	p.state = stateGround
	p.num = p.num[:0]
	p.body = p.body[:0]
}

// Feed consumes one chunk of raw output and returns every complete report it
// contains, in order. Incomplete trailing sequences are retained for the next
// call. Bytes that belong to other escape sequences (CSI, other OSC numbers,
// plain text) are ignored.
func (p *Parser) Feed(chunk []byte) []Event {
	var events []Event
	for _, b := range chunk {
		events = p.feedByte(b, events)
	}
	return events
}

// feedByte advances the state machine by one byte and appends any events to
// out. A malformed ESC inside a body is treated as a soft terminator, and the
// byte that followed it is reprocessed (so "ESC]7501;a=bESC]7501;c=d" yields
// two events).
func (p *Parser) feedByte(b byte, out []Event) []Event {
	for {
		switch p.state {
		case stateGround:
			if b == byteESC {
				p.state = stateESC
			}
			return out

		case stateESC:
			if b == byteLeftBracket {
				p.beginNumber()
				return out
			}
			// ESC followed by something else is not an OSC introducer. The
			// byte may still matter on its own (the next chunk could start a
			// sequence only at a byte boundary, which is exactly this one),
			// so reprocess it from ground.
			p.state = stateGround
			continue

		case stateNumber:
			switch {
			case b == byteSemicolon:
				if string(p.num) == oscNumber {
					p.state = stateBody
					p.body = p.body[:0]
				} else {
					p.state = stateIgnore
				}
				return out
			case b >= '0' && b <= '9':
				if len(p.num) >= maxNumberDigits {
					p.state = stateIgnore
					return out
				}
				p.num = append(p.num, b)
				return out
			default:
				// Malformed parameter; resync at ground, reprocessing the byte.
				p.state = stateGround
				continue
			}

		case stateIgnore:
			switch b {
			case bel:
				p.state = stateGround
			case byteESC:
				p.state = stateIgnoreESC
			}
			return out

		case stateIgnoreESC:
			p.state = stateGround
			switch b {
			case byteLeftBracket:
				p.beginNumber()
			case byteESC:
				p.state = stateIgnoreESC
			}
			return out

		case stateBody:
			switch {
			case b == bel:
				event, ok := p.finishBody()
				if ok {
					out = append(out, event)
				}
				return out
			case b == byteESC:
				p.state = stateBodyESC
				return out
			case len(p.body) >= maxBodyBytes:
				// Oversized payload: drop it and resync on the terminator.
				p.state = stateIgnore
				return out
			default:
				p.body = append(p.body, b)
				return out
			}

		case stateBodyESC:
			if b == byteBackslash {
				event, ok := p.finishBody()
				if ok {
					out = append(out, event)
				}
				return out
			}
			// An ESC that does not start ST is not part of the protocol.
			// Close the malformed sequence and reprocess the byte, keeping a
			// following ESC or `]` available as the start of the next sequence.
			if event, ok := p.finishBody(); ok {
				out = append(out, event)
			}
			if b == byteLeftBracket || b == byteESC {
				p.state = stateESC
			}
			continue
		}

		// Unreachable: every state returns or continues above.
		return out
	}
}

func (p *Parser) beginNumber() {
	p.state = stateNumber
	p.num = p.num[:0]
}

// finishBody decodes the accumulated payload and returns the parser to
// ground. A payload without a state key (and not a query) is malformed and
// reports ok=false.
func (p *Parser) finishBody() (Event, bool) {
	body := string(p.body)
	p.state = stateGround
	p.body = p.body[:0]
	p.num = p.num[:0]

	clock := p.now
	if clock == nil {
		clock = time.Now
	}
	event := Event{
		PaneID:     p.paneID,
		ObservedAt: clock(),
	}
	if strings.HasPrefix(body, queryPrefix) {
		event.Query = true
		body = body[len(queryPrefix):]
	}
	if body != "" {
		event.Values = make(map[string]string)
		for _, pair := range strings.Split(body, string(byteColon)) {
			if pair == "" {
				continue
			}
			key, value, found := strings.Cut(pair, string(byteEquals))
			if !found {
				event.Values[pair] = ""
				continue
			}
			event.Values[key] = value
		}
	}

	if !event.Query && event.Values["state"] == "" {
		return Event{}, false
	}
	event.State = State(event.Values["state"])
	event.App = event.Values["app"]
	event.Kind = event.Values["kind"]
	if raw := event.Values["msg"]; raw != "" {
		if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
			event.Message = string(decoded)
		}
	}
	return event, true
}
