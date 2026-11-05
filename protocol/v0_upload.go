package protocol

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/goplus/xgitd/pktline"
)

// DecodeError describes a request that could not be decoded.
type DecodeError struct {
	Op     string // operation being decoded, e.g. "upload-pack"
	Reason string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("protocol: %s: %s", e.Op, e.Reason)
}

func decodeErr(op, format string, args ...any) error {
	return &DecodeError{Op: op, Reason: fmt.Sprintf(format, args...)}
}

// DecodeUploadPackV0 decodes a single v0 fetch/clone negotiation round from r.
//
// The grammar it accepts is the client's half of the git-upload-pack
// conversation: a block of "want <oid> [caps]" lines (the first may carry a
// trailing capability list after a space), optional shallow/deepen/filter
// lines, a flush, then a block of "have <oid>" lines, optionally ended by
// "done". Over stateless HTTP each POST is one such round and is decoded on its
// own.
//
// The returned request's Wants is empty only for a well-formed but empty round
// (for example, an HTTP "flush only" probe); such a round still decodes without
// error so the handler can decide what to do.
func DecodeUploadPackV0(r io.Reader) (*UploadPackRequest, error) {
	const op = "upload-pack"
	pr := pktline.NewReader(r)
	req := &UploadPackRequest{}

	// Phase 1: want lines and their modifiers, terminated by a flush.
	firstWant := true
	sawPacket := false
	for {
		p, err := pr.ReadPacket()
		if err == io.EOF {
			// EOF before any packet is a clean empty round (a no-op probe). EOF
			// after one or more packets but before the terminating flush is a
			// truncated negotiation, which is an error.
			if !sawPacket {
				return req, nil
			}
			return nil, io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, err
		}
		sawPacket = true
		switch p.Kind {
		case pktline.KindFlush:
			// End of the want/command section; move to haves.
			goto haves
		case pktline.KindDelim, pktline.KindResponseEnd:
			return nil, decodeErr(op, "unexpected %s packet in want section", p.Kind)
		}

		line := trimLF(string(p.Data))
		keyword, rest, _ := strings.Cut(line, " ")
		switch keyword {
		case "want":
			oid := rest
			if firstWant {
				// The first want line may carry capabilities after the oid.
				id, caps, hasCaps := strings.Cut(rest, " ")
				oid = id
				if hasCaps {
					req.Capabilities = ParseCapabilities(caps)
				}
				firstWant = false
			}
			if req.Capabilities == nil {
				req.Capabilities = newCapabilities()
			}
			req.Wants = append(req.Wants, oid)
		case "shallow":
			req.Shallows = append(req.Shallows, rest)
		case "deepen":
			n, perr := strconv.Atoi(strings.TrimSpace(rest))
			if perr != nil {
				return nil, decodeErr(op, "bad deepen value %q", rest)
			}
			req.Deepen = n
		case "deepen-since":
			secs, perr := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if perr != nil {
				return nil, decodeErr(op, "bad deepen-since value %q", rest)
			}
			req.DeepenSince = secs
		case "deepen-not":
			req.DeepenNot = append(req.DeepenNot, rest)
		case "filter":
			req.Filter = rest
		default:
			req.Unknown = append(req.Unknown, line)
		}
	}

haves:
	if req.Capabilities == nil {
		req.Capabilities = newCapabilities()
	}
	for {
		p, err := pr.ReadPacket()
		if err == io.EOF {
			return req, nil
		}
		if err != nil {
			return nil, err
		}
		switch p.Kind {
		case pktline.KindFlush:
			// A second flush ends the have section without "done"; the client
			// expects another round.
			return req, nil
		case pktline.KindDelim, pktline.KindResponseEnd:
			return nil, decodeErr(op, "unexpected %s packet in have section", p.Kind)
		}

		line := trimLF(string(p.Data))
		keyword, rest, _ := strings.Cut(line, " ")
		switch keyword {
		case "have":
			req.Haves = append(req.Haves, rest)
		case "done":
			req.Done = true
			return req, nil
		default:
			req.Unknown = append(req.Unknown, line)
		}
	}
}

// trimLF removes a single trailing newline, the convention Git uses on most
// pkt-line payloads. It leaves payloads without a trailing newline untouched.
func trimLF(s string) string {
	return strings.TrimSuffix(s, "\n")
}
