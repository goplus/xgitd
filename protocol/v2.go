package protocol

import (
	"io"
	"strings"

	"github.com/goplus/xgitd/pktline"
)

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

// readV2Frame reads one v2 command frame from r.
func readV2Frame(r io.Reader) (*v2Frame, error) {
	const op = "v2"
	pr := pktline.NewReader(r)
	f := &v2Frame{Capabilities: newCapabilities()}

	// Section 0 = capabilities (before delim), 1 = arguments (after delim).
	section := 0
	sawCommand := false
	for {
		p, err := pr.ReadPacket()
		if err == io.EOF {
			if !sawCommand {
				return nil, decodeErr(op, "no command")
			}
			return f, nil
		}
		if err != nil {
			return nil, err
		}
		switch p.Kind {
		case pktline.KindFlush:
			if !sawCommand {
				return nil, decodeErr(op, "flush before command")
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

// DecodeV2 reads one v2 command frame and decodes it into the matching request
// type. The returned value is one of *LsRefsRequest, *FetchRequest, or
// *ObjectInfoRequest. The command name is also returned for routing.
func DecodeV2(r io.Reader) (command string, req any, err error) {
	f, err := readV2Frame(r)
	if err != nil {
		return "", nil, err
	}
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
