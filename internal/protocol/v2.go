package protocol

import (
	"errors"
	"io"
	"strings"

	"github.com/goplus/xgitd/internal/pktline"
)

// ErrNoMoreCommands is returned by [V2Decoder.Next] when the client ends a v2
// session by sending a flush-pkt where the next command=... line would begin.
// It signals a clean end of the command sequence, not an error.
var ErrNoMoreCommands = errors.New("protocol: no more v2 commands")

// v2Frame is the common shape of a v2 command: a command name, a block of
// capability lines, and a block of argument lines.
//
// On the wire it is:
//
//	command=<name>
//	<capability>...
//	0001            (delim)
//	<argument>...
//	0000            (flush)
//
// The capability block and the delimiter are optional in practice (a command
// with no capabilities may send the arguments straight after command=), so the
// reader tolerates arguments appearing before any delimiter.
type v2Frame struct {
	Command      string
	Capabilities *Capabilities
	Args         []string
}

// readV2FrameFrom reads one v2 command frame from an existing pkt-line reader.
//
// A flush-pkt before any command=... line means the client has ended the
// command sequence, which is reported as [ErrNoMoreCommands]. This is how a
// stateful transport (SSH) learns that the client is done after one or more
// request/response rounds on the same connection.
func readV2FrameFrom(pr *pktline.Reader) (*v2Frame, error) {
	const op = "v2"
	f := &v2Frame{Capabilities: newCapabilities()}

	// Section 0 = capabilities (before delim), 1 = arguments (after delim).
	section := 0
	sawCommand := false
	for {
		p, err := pr.ReadPacket()
		if err == io.EOF {
			if !sawCommand {
				// Stream closed with nothing pending: treat the same as an
				// explicit flush terminating the sequence.
				return nil, ErrNoMoreCommands
			}
			return f, nil
		}
		if err != nil {
			return nil, err
		}
		switch p.Kind {
		case pktline.KindFlush:
			if !sawCommand {
				// A flush where a command would begin ends the sequence.
				return nil, ErrNoMoreCommands
			}
			return f, nil
		case pktline.KindDelim:
			section = 1
			continue
		case pktline.KindResponseEnd:
			return nil, decodeErr(op, "unexpected response-end packet")
		}

		line := trimLF(string(p.Data))
		if name, ok := strings.CutPrefix(line, "command="); ok {
			f.Command = name
			sawCommand = true
			continue
		}
		if !sawCommand {
			return nil, decodeErr(op, "first line %q is not command=", line)
		}
		if section == 0 {
			f.Capabilities.add(line)
		} else {
			f.Args = append(f.Args, line)
		}
	}
}

// V2Decoder reads successive v2 commands from a single stream, so a stateful
// transport can handle several commands (for example ls-refs then fetch) in one
// connection. It holds one pkt-line reader across commands, which is required
// because a fresh reader would buffer-ahead and lose bytes between commands.
type V2Decoder struct {
	pr *pktline.Reader
}

// NewV2Decoder returns a decoder reading from r.
func NewV2Decoder(r io.Reader) *V2Decoder {
	return &V2Decoder{pr: pktline.NewReader(r)}
}

// Next decodes the next v2 command. It returns the command name, the decoded
// request (one of *LsRefsRequest, *FetchRequest, *ObjectInfoRequest), or
// [ErrNoMoreCommands] when the client has ended the sequence.
func (d *V2Decoder) Next() (command string, req any, err error) {
	f, err := readV2FrameFrom(d.pr)
	if err != nil {
		return "", nil, err
	}
	return routeV2Frame(f)
}

// routeV2Frame decodes a frame into its typed request.
func routeV2Frame(f *v2Frame) (command string, req any, err error) {
	switch f.Command {
	case "ls-refs":
		return f.Command, decodeLsRefs(f), nil
	case "fetch":
		req, err := decodeFetch(f)
		return f.Command, req, err
	case "object-info":
		req, err := decodeObjectInfo(f)
		return f.Command, req, err
	default:
		return f.Command, nil, decodeErr("v2", "unknown command %q", f.Command)
	}
}

// DecodeV2 reads one v2 command frame and decodes it into the matching request
// type. The returned value is one of *LsRefsRequest, *FetchRequest, or
// *ObjectInfoRequest. The command name is also returned for routing.
//
// It decodes exactly one command, which is what a stateless transport (one
// command per HTTP POST) needs. A stateful transport that may receive several
// commands on one connection should use [V2Decoder] instead.
func DecodeV2(r io.Reader) (command string, req any, err error) {
	f, err := readV2FrameFrom(pktline.NewReader(r))
	if err != nil {
		if errors.Is(err, ErrNoMoreCommands) {
			// Preserve the previous behavior for a bare flush: a single-command
			// decode of an empty sequence is a protocol error.
			return "", nil, decodeErr("v2", "flush before command")
		}
		return "", nil, err
	}
	return routeV2Frame(f)
}
