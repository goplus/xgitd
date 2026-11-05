package pktline

import (
	"bufio"
	"io"
)

// Packet is a single pkt-line returned by [Reader.ReadPacket].
//
// For [KindData] the Data field holds the payload (without the length prefix);
// for the special kinds it is nil. Data aliases an internal buffer that is
// reused by the next read, so callers that need to retain it must copy it.
type Packet struct {
	Kind Kind
	Data []byte
}

// Reader reads pkt-lines from an underlying stream.
//
// A Reader is not safe for concurrent use. The payload returned by ReadPacket
// is only valid until the next call.
type Reader struct {
	r   *bufio.Reader
	buf []byte // reused payload buffer
	hdr [HeaderLen]byte
}

// NewReader returns a [Reader] reading from r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(r)}
}

// ReadPacket reads the next pkt-line.
//
// At a clean end of stream it returns io.EOF. A header that begins but does not
// complete, or a data packet whose payload is truncated, returns
// io.ErrUnexpectedEOF. A malformed or reserved length header returns an
// [*ErrProtocol].
func (r *Reader) ReadPacket() (Packet, error) {
	if _, err := io.ReadFull(r.r, r.hdr[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			// Some header bytes arrived but not all four.
			return Packet{}, io.ErrUnexpectedEOF
		}
		// err == io.EOF with no bytes read is a clean end of stream.
		return Packet{}, err
	}

	length := 0
	for _, b := range r.hdr {
		d := hexDigit(b)
		if d < 0 {
			return Packet{}, protoErr("invalid length prefix %q", string(r.hdr[:]))
		}
		length = length<<4 | d
	}

	switch length {
	case flushLen:
		return Packet{Kind: KindFlush}, nil
	case delimLen:
		return Packet{Kind: KindDelim}, nil
	case responseEndLen:
		return Packet{Kind: KindResponseEnd}, nil
	case 3:
		return Packet{}, protoErr("reserved length 0003")
	}

	if length < HeaderLen {
		// Unreachable given the cases above, but keep the invariant explicit.
		return Packet{}, protoErr("invalid length %04x", length)
	}
	if length > MaxPacketLen {
		return Packet{}, protoErr("length %d exceeds maximum %d", length, MaxPacketLen)
	}

	payloadLen := length - HeaderLen
	if cap(r.buf) < payloadLen {
		r.buf = make([]byte, payloadLen)
	}
	r.buf = r.buf[:payloadLen]
	if payloadLen > 0 {
		if _, err := io.ReadFull(r.r, r.buf); err != nil {
			if err == io.EOF {
				return Packet{}, io.ErrUnexpectedEOF
			}
			return Packet{}, err
		}
	}
	return Packet{Kind: KindData, Data: r.buf}, nil
}

// ReadPacketString is a convenience wrapper around [Reader.ReadPacket] that
// returns the payload of a data packet as a string. For non-data packets it
// returns the empty string and the packet's kind. Note that the reported kind
// is only meaningful when err is nil.
func (r *Reader) ReadPacketString() (string, Kind, error) {
	p, err := r.ReadPacket()
	if err != nil {
		return "", p.Kind, err
	}
	if p.Kind != KindData {
		return "", p.Kind, nil
	}
	return string(p.Data), p.Kind, nil
}

// Remaining returns a reader over the bytes that follow the last pkt-line read,
// including any data the Reader has buffered internally but not yet returned.
// After calling Remaining the Reader must not be used again.
//
// This is how a transport hands the raw tail of a stream (for example a push's
// packfile) to code that reads it directly, without losing bytes the pkt-line
// reader buffered ahead.
func (r *Reader) Remaining() io.Reader {
	return r.r // *bufio.Reader already holds buffered + underlying bytes.
}
