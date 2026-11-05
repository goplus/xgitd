package protocol

import (
	"io"
	"strings"

	"github.com/goplus/xgitd/pktline"
)

// DecodeReceivePackV0 decodes a v0 push (git-receive-pack) from r.
//
// The client sends a block of ref-update commands, each
// "<old-oid> <new-oid> <ref>", with capabilities riding after a NUL on the
// first command. The command list is terminated by a flush. If the client
// negotiated the push-options capability, a block of option lines terminated by
// a flush follows. The packfile (if any) is whatever remains on the stream.
//
// The returned request's Packfile is positioned at the start of the pack data
// and is left unread: the handler decides whether to consume it. When the push
// consists only of deletions the client may send no pack at all; the Packfile
// then yields EOF immediately, so it is reported as nil only when the stream is
// already at EOF after the command/option sections.
func DecodeReceivePackV0(r io.Reader) (*ReceivePackRequest, error) {
	const op = "receive-pack"
	pr := pktline.NewReader(r)
	req := &ReceivePackRequest{Capabilities: newCapabilities()}

	first := true
	sawFlush := false
	for {
		p, err := pr.ReadPacket()
		if err == io.EOF {
			if first {
				// Empty push: no commands at all.
				return req, nil
			}
			return nil, io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, err
		}
		if p.Kind == pktline.KindFlush {
			sawFlush = true
			break
		}
		if p.Kind != pktline.KindData {
			return nil, decodeErr(op, "unexpected %s packet in command section", p.Kind)
		}

		line := trimLF(string(p.Data))
		if first {
			// Capabilities follow a NUL after the first command.
			if cmd, caps, ok := strings.Cut(line, "\x00"); ok {
				line = cmd
				req.Capabilities = ParseCapabilities(caps)
			}
			first = false
		}

		cmd, perr := parseRefUpdate(op, line)
		if perr != nil {
			return nil, perr
		}
		req.Commands = append(req.Commands, cmd)
	}

	// Optional push-options section, only when negotiated.
	if sawFlush && req.Capabilities.Has("push-options") {
		for {
			p, err := pr.ReadPacket()
			if err == io.EOF {
				return req, nil
			}
			if err != nil {
				return nil, err
			}
			if p.Kind == pktline.KindFlush {
				break
			}
			if p.Kind != pktline.KindData {
				return nil, decodeErr(op, "unexpected %s packet in push-options section", p.Kind)
			}
			req.PushOptions = append(req.PushOptions, trimLF(string(p.Data)))
		}
	}

	// The remainder of the stream is the packfile. Peek one byte so a push with
	// no pack reports Packfile == nil rather than a reader that yields EOF.
	peek, hasData, err := peekReader(pr.Remaining())
	if err != nil {
		return nil, err
	}
	if hasData {
		req.Packfile = peek
	}
	return req, nil
}

// parseRefUpdate parses one "<old> <new> <ref>" command line.
func parseRefUpdate(op, line string) (RefUpdate, error) {
	fields := strings.SplitN(line, " ", 3)
	if len(fields) != 3 {
		return RefUpdate{}, decodeErr(op, "malformed ref-update command %q", line)
	}
	return RefUpdate{
		OldID: fields[0],
		NewID: fields[1],
		Ref:   fields[2],
	}, nil
}
