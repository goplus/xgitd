package protocol

import (
	"io"
	"strconv"
	"strings"

	"github.com/goplus/xgitd/pktline"
)

// ObjectInfoEntry is the server's answer for one requested object in an
// object-info response.
type ObjectInfoEntry struct {
	// OID is the object ID this entry describes.
	OID ObjectID
	// Size is the object's size in bytes, reported when the request asked for
	// the "size" attribute. A negative size marks the object as missing, which
	// is reported to the client as a bare oid with no size field.
	Size int64
}

// WriteObjectInfo writes a v2 object-info response.
//
// The first line echoes the requested attributes (for example "size"), then one
// line per object follows: "<oid> <size>" when the size attribute was requested,
// or just "<oid>" otherwise or when the object is missing (Size < 0). The
// response ends with a flush-pkt.
//
// The req determines which attribute columns are written; the handler supplies
// the entries.
func WriteObjectInfo(w io.Writer, req *ObjectInfoRequest, entries []ObjectInfoEntry) error {
	pw := pktline.NewWriter(w)

	wantSize := false
	for _, attr := range req.Attributes {
		if attr == "size" {
			wantSize = true
		}
	}

	// The response begins with the attribute-column header line, echoing the
	// attributes the client requested. The v2 grammar makes this line the
	// mandatory first line of the response, so it is written unconditionally
	// (empty when the client requested no attributes).
	if err := pw.WriteString(strings.Join(req.Attributes, " ") + "\n"); err != nil {
		return err
	}

	for _, e := range entries {
		line := e.OID
		if wantSize {
			if e.Size < 0 {
				// Missing object: report the oid with no size.
			} else {
				line += " " + strconv.FormatInt(e.Size, 10)
			}
		}
		if err := pw.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return pw.Flush()
}
