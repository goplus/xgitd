package pktline

import (
	"io"
)

// Writer frames payloads as pkt-lines onto an underlying stream.
//
// A Writer is not safe for concurrent use.
type Writer struct {
	w io.Writer
}

// NewWriter returns a [Writer] writing to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Raw returns the underlying writer, for payloads that must bypass pkt-line
// framing — most notably a v0 upload-pack packfile sent without side-band, which
// follows the pkt-line negotiation as a raw byte stream. Interleaving Raw writes
// with framed writes on the same Writer is the caller's responsibility.
func (w *Writer) Raw() io.Writer { return w.w }

// writeControl writes a special zero-payload pkt-line.
func (w *Writer) writeControl(length int) error {
	var hdr [HeaderLen]byte
	encodeLen(&hdr, length)
	_, err := w.w.Write(hdr[:])
	return err
}

// Flush writes a flush-pkt (0000).
func (w *Writer) Flush() error { return w.writeControl(flushLen) }

// Delim writes a delim-pkt (0001).
func (w *Writer) Delim() error { return w.writeControl(delimLen) }

// ResponseEnd writes a response-end-pkt (0002).
func (w *Writer) ResponseEnd() error { return w.writeControl(responseEndLen) }

// WritePacket frames p as a single data packet and writes it.
//
// It returns an error if p is empty (an empty data packet is indistinguishable
// from a flush) or longer than [MaxPayloadLen].
func (w *Writer) WritePacket(p []byte) error {
	if len(p) == 0 {
		return protoErr("refusing to write empty data packet (use Flush)")
	}
	if len(p) > MaxPayloadLen {
		return protoErr("payload length %d exceeds maximum %d", len(p), MaxPayloadLen)
	}
	var hdr [HeaderLen]byte
	encodeLen(&hdr, len(p)+HeaderLen)
	if _, err := w.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.w.Write(p)
	return err
}

// WriteString frames s as a single data packet. Callers that want the Git
// convention of a trailing newline must include it in s.
func (w *Writer) WriteString(s string) error {
	return w.WritePacket([]byte(s))
}

const hexDigits = "0123456789abcdef"

// encodeLen writes length as four lowercase hex digits into hdr.
func encodeLen(hdr *[HeaderLen]byte, length int) {
	hdr[0] = hexDigits[(length>>12)&0xf]
	hdr[1] = hexDigits[(length>>8)&0xf]
	hdr[2] = hexDigits[(length>>4)&0xf]
	hdr[3] = hexDigits[length&0xf]
}
