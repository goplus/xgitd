package protocol

import (
	"bytes"
	"io"

	"github.com/goplus/xgitd/internal/pktline"
)

// RefStatus is the outcome of one ref update in a push, reported back to the
// client in the report-status response.
type RefStatus struct {
	// Ref is the full ref name the status is for.
	Ref string
	// OK reports whether the update succeeded. When false, Reason explains why
	// and is sent to the client.
	OK bool
	// Reason is the human-readable failure reason for a rejected update. It is
	// ignored when OK is true.
	Reason string
}

// ReportStatus is the git-receive-pack report-status response: whether the
// packfile unpacked, and the per-ref outcome. A handler builds one after
// processing a push and writes it with WriteV0.
type ReportStatus struct {
	// UnpackOK reports whether the packfile was unpacked successfully. When
	// false, UnpackError is sent as the reason.
	UnpackOK bool
	// UnpackError is the reason the unpack failed; ignored when UnpackOK is
	// true. When empty on a failure, a generic reason is sent.
	UnpackError string
	// Refs is the per-ref outcome, in the order to report.
	Refs []RefStatus
}

// WriteV0 writes the report-status response to w.
//
// The report is a block of pkt-lines — "unpack ok" or "unpack <error>", then
// "ok <ref>" or "ng <ref> <reason>" per ref — terminated by a flush. When
// sideband is true (the client negotiated side-band-64k) the whole report is
// multiplexed on channel 1, as git does for a side-band push; otherwise it is
// written directly.
func (rs ReportStatus) WriteV0(w io.Writer, sideband bool) error {
	// Build the report into a buffer first, then emit it either raw or wrapped
	// in side-band framing.
	var body bytes.Buffer
	pw := pktline.NewWriter(&body)

	unpack := "unpack ok\n"
	if !rs.UnpackOK {
		reason := rs.UnpackError
		if reason == "" {
			reason = "unpack failed"
		}
		unpack = "unpack " + reason + "\n"
	}
	if err := pw.WriteString(unpack); err != nil {
		return err
	}
	for _, r := range rs.Refs {
		var line string
		if r.OK {
			line = "ok " + r.Ref + "\n"
		} else {
			reason := r.Reason
			if reason == "" {
				reason = "rejected"
			}
			line = "ng " + r.Ref + " " + reason + "\n"
		}
		if err := pw.WriteString(line); err != nil {
			return err
		}
	}
	if err := pw.Flush(); err != nil {
		return err
	}

	if sideband {
		outer := pktline.NewWriter(w)
		sb := pktline.NewSideBandWriter(outer)
		if _, err := sb.WriteData(body.Bytes()); err != nil {
			return err
		}
		return outer.Flush()
	}
	_, err := w.Write(body.Bytes())
	return err
}
