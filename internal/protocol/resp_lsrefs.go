package protocol

import (
	"io"

	"github.com/goplus/xgitd/internal/pktline"
)

// WriteLsRefs writes a v2 ls-refs response: one line per ref, terminated by a
// flush-pkt.
//
// Each line is "<oid> <name>", optionally followed by space-separated
// attributes: "symref-target:<target>" when the ref is symbolic and the request
// asked for symrefs, and "peeled:<peeled-oid>" for an annotated tag when the
// request asked to peel. An unborn ref (req.Unborn) is written with the
// "unborn" object-id placeholder and a symref-target attribute.
//
// The req controls which attributes are emitted, matching what the client asked
// for; the handler supplies the refs.
func WriteLsRefs(w io.Writer, req *LsRefsRequest, refs []Ref) error {
	pw := pktline.NewWriter(w)
	for _, ref := range refs {
		line := ref.ID + " " + ref.Name
		if ref.Unborn && req.Unborn {
			line = "unborn " + ref.Name
		}
		if req.Symrefs && ref.SymrefTarget != "" {
			line += " symref-target:" + ref.SymrefTarget
		}
		if req.Peel && ref.PeeledID != "" {
			line += " peeled:" + ref.PeeledID
		}
		if err := pw.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return pw.Flush()
}
