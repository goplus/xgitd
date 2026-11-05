package protocol

import (
	"errors"
	"io"

	"github.com/goplus/xgitd/internal/pktline"
)

// ErrNegotiationIncomplete is returned by the section methods that must precede
// or be the packfile (shallow-info, wanted-refs, packfile) when the handler has
// written an acknowledgments section that did not declare "ready". Per the v2
// fetch grammar such a response is negotiation-only: it ends after the
// acknowledgments section and carries no packfile, so the handler must Close the
// responder instead of continuing.
var ErrNegotiationIncomplete = errors.New("protocol: fetch acknowledgments without ready cannot be followed by a packfile")

// FetchResponder writes the server side of a v2 fetch command.
//
// A v2 fetch response is a sequence of named sections, each introduced by a
// section-header pkt-line and separated by a delim-pkt, ending with a flush-pkt:
//
//	acknowledgments    (optional; during negotiation)
//	shallow-info       (optional)
//	wanted-refs        (optional; when the client sent want-ref)
//	packfile-uris      (optional)
//	packfile           (the pack, side-band multiplexed)
//
// The responder enforces the delim-between-sections framing so a handler only
// chooses which sections to emit and provides their content. Call the section
// methods in protocol order, then [FetchResponder.Close] (or let PackSection's
// finish do the final flush).
//
// A typical full-clone response is a single packfile section: no negotiation
// acknowledgements are needed, so the handler calls PackSection directly.
//
// Negotiation rule: if a handler writes an acknowledgments section without
// "ready" (the server needs another round and sends no pack), the only valid
// continuation is Close. Attempting a shallow-info, wanted-refs, or packfile
// section after that returns [ErrNegotiationIncomplete].
type FetchResponder struct {
	pw         *pktline.Writer
	wroteFirst bool // whether any section has been started (for delim framing)
	// ackedNotReady records that an acknowledgments section was written without
	// a "ready" line, after which the protocol forbids a packfile.
	ackedNotReady bool
	closed        bool
}

// NewFetchResponder returns a responder writing to w.
func NewFetchResponder(w io.Writer) *FetchResponder {
	return &FetchResponder{pw: pktline.NewWriter(w)}
}

// beginSection writes the delim that separates this section from the previous
// one (for every section after the first) and the section header line.
func (r *FetchResponder) beginSection(header string) error {
	if r.wroteFirst {
		if err := r.pw.Delim(); err != nil {
			return err
		}
	}
	r.wroteFirst = true
	return r.pw.WriteString(header + "\n")
}

// requirePackAllowed reports an error when a section that must precede or be the
// packfile is attempted after a non-ready acknowledgments section.
func (r *FetchResponder) requirePackAllowed() error {
	if r.ackedNotReady {
		return ErrNegotiationIncomplete
	}
	return nil
}

// Acknowledgments writes the acknowledgments section.
//
// acks is the list of common object IDs to acknowledge with "ACK <oid>". When
// ready is true a trailing "ready" line is written (the server has enough common
// history to build the pack). When acks is empty and ready is false, a single
// "NAK" line is written (no common objects yet).
func (r *FetchResponder) Acknowledgments(acks []ObjectID, ready bool) error {
	if err := r.beginSection("acknowledgments"); err != nil {
		return err
	}
	if !ready {
		r.ackedNotReady = true
	}
	if len(acks) == 0 && !ready {
		return r.pw.WriteString("NAK\n")
	}
	for _, oid := range acks {
		if err := r.pw.WriteString("ACK " + oid + "\n"); err != nil {
			return err
		}
	}
	if ready {
		return r.pw.WriteString("ready\n")
	}
	return nil
}

// ShallowInfo writes the shallow-info section: a "shallow <oid>" line per entry
// in shallows and an "unshallow <oid>" line per entry in unshallows.
func (r *FetchResponder) ShallowInfo(shallows, unshallows []ObjectID) error {
	if err := r.requirePackAllowed(); err != nil {
		return err
	}
	if err := r.beginSection("shallow-info"); err != nil {
		return err
	}
	for _, oid := range shallows {
		if err := r.pw.WriteString("shallow " + oid + "\n"); err != nil {
			return err
		}
	}
	for _, oid := range unshallows {
		if err := r.pw.WriteString("unshallow " + oid + "\n"); err != nil {
			return err
		}
	}
	return nil
}

// WantedRef pairs a want-ref name with the object ID it resolved to, for the
// wanted-refs section.
type WantedRef struct {
	Name string
	ID   ObjectID
}

// WantedRefs writes the wanted-refs section, one "<oid> <name>" line per ref.
// It is the server's answer to the client's "want-ref" lines.
func (r *FetchResponder) WantedRefs(refs []WantedRef) error {
	if err := r.requirePackAllowed(); err != nil {
		return err
	}
	if err := r.beginSection("wanted-refs"); err != nil {
		return err
	}
	for _, ref := range refs {
		if err := r.pw.WriteString(ref.ID + " " + ref.Name + "\n"); err != nil {
			return err
		}
	}
	return nil
}

// PackSection begins the packfile section and returns the writer for the pack
// bytes together with a finish function that writes the terminating flush-pkt.
//
// The pack is multiplexed on side-band-64k: channel 1 for pack data (the
// returned writer), and the responder's Progress/Error for channels 2 and 3.
// v2 always uses side-band for the packfile, so there is no raw variant.
//
// The handler produces valid packfile bytes; the responder only frames them.
func (r *FetchResponder) PackSection() (w io.Writer, finish func() error, err error) {
	if err := r.requirePackAllowed(); err != nil {
		return nil, nil, err
	}
	if err := r.beginSection("packfile"); err != nil {
		return nil, nil, err
	}
	sb := pktline.NewSideBandWriter(r.pw)
	return sb, func() error {
		r.closed = true
		return r.pw.Flush()
	}, nil
}

// Progress writes a progress message on side-band channel 2. It is only valid
// after PackSection has been called, since progress rides the same side-band
// stream as the pack.
func (r *FetchResponder) Progress(msg string) error {
	sb := pktline.NewSideBandWriter(r.pw)
	return sb.WriteProgress([]byte(msg))
}

// Error writes a fatal error message on side-band channel 3; the client aborts
// after printing it. Valid only within the packfile section.
func (r *FetchResponder) Error(msg string) error {
	sb := pktline.NewSideBandWriter(r.pw)
	return sb.WriteError([]byte(msg))
}

// Close writes the terminating flush-pkt if the packfile section's finish
// function has not already done so (for example a negotiation-only response that
// ends after the acknowledgments section). It is safe to call more than once.
func (r *FetchResponder) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.pw.Flush()
}
