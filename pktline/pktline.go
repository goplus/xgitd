// Package pktline implements the pkt-line framing used by the Git wire
// protocols.
//
// Every unit of a Git conversation is a pkt-line: four hexadecimal digits
// giving the total length of the line (the four digits included), followed by
// the payload. Three length values are special and carry no payload:
//
//	0000  flush-pkt         (ends a section)
//	0001  delim-pkt         (separates a v2 command's capabilities from its args)
//	0002  response-end-pkt  (ends a stateless-rpc response)
//
// The largest allowed pkt-line is [MaxPacketLen] bytes in total, so the largest
// payload a single data packet may carry is [MaxPayloadLen]. A declared length
// of 0003 (or any value in 3..3) is reserved and treated as a protocol error.
package pktline

import (
	"fmt"
)

// Framing constants.
const (
	// MaxPacketLen is the largest total size of a pkt-line, including its
	// four-digit length prefix.
	MaxPacketLen = 65520
	// HeaderLen is the size of the four-digit hex length prefix.
	HeaderLen = 4
	// MaxPayloadLen is the largest payload a single data packet may carry.
	MaxPayloadLen = MaxPacketLen - HeaderLen
)

// Special length values. These are the only lengths below [HeaderLen] that are
// valid; a declared length of 3 is reserved and is a protocol error.
const (
	flushLen       = 0
	delimLen       = 1
	responseEndLen = 2
)

// Kind classifies a pkt-line read by a [Reader].
type Kind int

const (
	// KindData is an ordinary data packet carrying a payload.
	KindData Kind = iota
	// KindFlush is a flush-pkt (0000).
	KindFlush
	// KindDelim is a delim-pkt (0001).
	KindDelim
	// KindResponseEnd is a response-end-pkt (0002).
	KindResponseEnd
)

func (k Kind) String() string {
	switch k {
	case KindData:
		return "data"
	case KindFlush:
		return "flush"
	case KindDelim:
		return "delim"
	case KindResponseEnd:
		return "response-end"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// ErrProtocol reports a malformed pkt-line: a bad length header or a reserved
// length value.
type ErrProtocol struct {
	// Reason is a human-readable description of the fault.
	Reason string
}

func (e *ErrProtocol) Error() string { return "pktline: " + e.Reason }

func protoErr(format string, args ...any) error {
	return &ErrProtocol{Reason: fmt.Sprintf(format, args...)}
}

// hexDigit decodes a single ASCII lowercase/uppercase hex digit. It returns -1
// for non-hex bytes.
func hexDigit(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}
