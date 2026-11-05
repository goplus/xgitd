package protocol

import (
	"io"

	"github.com/goplus/xgitd/pktline"
)

// UploadPackResponder writes the server side of a v0 git-upload-pack
// negotiation and the packfile that follows.
//
// A handler creates one with [NewUploadPackResponder], reports the negotiation
// result with the ACK/NAK methods, and then streams the packfile with
// [UploadPackResponder.PackWriter]. The responder frames everything as
// pkt-lines and, when the client negotiated side-band-64k, multiplexes the
// packfile and progress onto the side-band channels.
type UploadPackResponder struct {
	pw       *pktline.Writer
	sideband bool
}

// NewUploadPackResponder returns a responder writing to w. If sideband is true
// the packfile is multiplexed on side-band-64k (channel 1) and progress on
// channel 2; otherwise the packfile bytes are written raw after the
// negotiation. A handler usually passes req.Capabilities.Has("side-band-64k").
func NewUploadPackResponder(w io.Writer, sideband bool) *UploadPackResponder {
	return &UploadPackResponder{pw: pktline.NewWriter(w), sideband: sideband}
}

// NAK writes a single "NAK" line, the response when the server shares none of
// the client's haves (a full clone, or a round with no common commits).
func (r *UploadPackResponder) NAK() error {
	return r.pw.WriteString("NAK\n")
}

// ACK writes "ACK <oid>", the final acknowledgement that negotiation is
// complete and the named common object is the basis for the pack.
func (r *UploadPackResponder) ACK(oid ObjectID) error {
	return r.pw.WriteString("ACK " + oid + "\n")
}

// ACKContinue writes "ACK <oid> continue", used during multi-ack negotiation to
// acknowledge a common object while expecting the client to keep sending haves.
func (r *UploadPackResponder) ACKContinue(oid ObjectID) error {
	return r.pw.WriteString("ACK " + oid + " continue\n")
}

// ACKCommon writes "ACK <oid> common", used during multi_ack_detailed
// negotiation to mark a common object.
func (r *UploadPackResponder) ACKCommon(oid ObjectID) error {
	return r.pw.WriteString("ACK " + oid + " common\n")
}

// ACKReady writes "ACK <oid> ready", the multi_ack_detailed signal that the
// server has enough common history and is ready to send the pack.
func (r *UploadPackResponder) ACKReady(oid ObjectID) error {
	return r.pw.WriteString("ACK " + oid + " ready\n")
}

// WriteShallow writes a "shallow <oid>" line, informing the client that the
// object is a shallow boundary of the pack about to be sent.
func (r *UploadPackResponder) WriteShallow(oid ObjectID) error {
	return r.pw.WriteString("shallow " + oid + "\n")
}

// WriteUnshallow writes an "unshallow <oid>" line, telling the client an object
// that was a shallow boundary now has its ancestry included.
func (r *UploadPackResponder) WriteUnshallow(oid ObjectID) error {
	return r.pw.WriteString("unshallow " + oid + "\n")
}

// Flush writes a flush-pkt, used to terminate the shallow/unshallow section
// before the acknowledgements.
func (r *UploadPackResponder) Flush() error {
	return r.pw.Flush()
}

// PackWriter returns the writer for the packfile section and a finish function
// that must be called when the pack is fully written.
//
// With side-band negotiated, the returned writer multiplexes pack bytes onto
// channel 1 (and the sideband's WriteProgress can be used for channel 2); the
// finish function writes the terminating flush-pkt. Without side-band, the
// writer passes pack bytes through raw and finish is a no-op, because the pack
// stream ends on its own and the transport closes the connection.
//
// The handler is responsible for producing valid packfile bytes; the responder
// only frames them.
func (r *UploadPackResponder) PackWriter() (w io.Writer, finish func() error) {
	if r.sideband {
		sb := pktline.NewSideBandWriter(r.pw)
		return sb, func() error { return r.pw.Flush() }
	}
	return r.pw.Raw(), func() error { return nil }
}

// Progress writes a human-readable progress message. It is only delivered to
// the client when side-band was negotiated (channel 2); without side-band it is
// silently dropped, because there is no channel to carry it alongside the pack.
func (r *UploadPackResponder) Progress(msg string) error {
	if !r.sideband {
		return nil
	}
	sb := pktline.NewSideBandWriter(r.pw)
	return sb.WriteProgress([]byte(msg))
}

// Error writes a fatal error message to the client. With side-band it is sent on
// channel 3 (the client aborts after printing it); without side-band it is sent
// as an ERR pkt-line.
func (r *UploadPackResponder) Error(msg string) error {
	if r.sideband {
		sb := pktline.NewSideBandWriter(r.pw)
		return sb.WriteError([]byte(msg))
	}
	return r.pw.WriteString("ERR " + msg + "\n")
}
