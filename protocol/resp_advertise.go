package protocol

import (
	"io"
	"sort"
	"strings"

	"github.com/goplus/xgitd/pktline"
)

// ZeroID is the all-zero object ID used on the wire to mean "no object": the old
// ID of a created ref, the new ID of a deleted ref, and the ID advertised for an
// empty repository. It is returned at SHA-1 width; use [ZeroIDFor] for a specific
// object format.
const ZeroID = "0000000000000000000000000000000000000000"

// ZeroIDFor returns the all-zero object ID at the width of the given
// object-format name (SHA-1 unless the name is sha256).
func ZeroIDFor(format string) string {
	return strings.Repeat("0", hexLenForFormat(format))
}

// Ref is one reference to advertise. It is used by both the v0 ref
// advertisement ([WriteRefAdvertisementV0]) and the v2 ls-refs response
// ([WriteLsRefs]).
type Ref struct {
	// Name is the full ref name, e.g. "refs/heads/main" or "HEAD".
	Name string
	// ID is the hex object ID the ref points at.
	ID ObjectID
	// SymrefTarget, when non-empty, is the ref this symbolic ref points to
	// (e.g. HEAD -> "refs/heads/main"). In v0 it becomes a "symref=" capability;
	// in v2 it becomes a "symref-target:" attribute when the client asked for
	// symrefs.
	SymrefTarget string
	// PeeledID, when non-empty, is the object an annotated tag ultimately points
	// to. In v0 it is advertised as an extra "<peeled> <name>^{}" line; in v2 it
	// becomes a "peeled:" attribute when the client asked to peel.
	PeeledID ObjectID
	// Unborn marks a ref (typically HEAD) that exists symbolically but has no
	// commit yet. Only meaningful for v2 ls-refs with the "unborn" argument.
	Unborn bool
}

// RefAdvertisementV0 describes a v0 reference advertisement. A handler builds
// one in its AdvertiseRefs method and writes it with WriteV0 so the pkt-line
// framing, capability placement, and empty-repository case are all handled
// correctly.
type RefAdvertisementV0 struct {
	// Service is the service the advertisement is for, "git-upload-pack" or
	// "git-receive-pack". It is not written on the wire here but may inform the
	// capability set a handler chooses.
	Service string
	// Refs are the refs to advertise. For a non-empty repository the first ref
	// carries the capability list after a NUL; the order is preserved as given,
	// except that the refs are sorted by name for a stable advertisement when
	// SortRefs is true.
	Refs []Ref
	// Capabilities are the server capabilities to advertise on the first line
	// (for example "report-status", "side-band-64k", "ofs-delta",
	// "object-format=sha1", "agent=..."). Symref capabilities for refs with a
	// SymrefTarget are appended automatically.
	Capabilities []string
	// ObjectFormat is the repository's object format, used only to size the
	// zero ID advertised for an empty repository. Empty means SHA-1.
	ObjectFormat string
	// SortRefs sorts Refs by name before writing. Git clients do not require
	// sorted refs, but a stable order makes advertisements reproducible.
	SortRefs bool
}

// WriteV0 writes the advertisement to w using pkt-line framing.
//
// For a repository with refs, the first line is "<id> <name>\x00<caps>\n" and
// subsequent lines are "<id> <name>\n"; annotated tags with a PeeledID also emit
// a "<peeled> <name>^{}\n" line. For an empty repository (no refs) the special
// "<zero-id> capabilities^{}\x00<caps>\n" line is written so the client still
// learns the server's capabilities. The advertisement ends with a flush-pkt.
func (a RefAdvertisementV0) WriteV0(w io.Writer) error {
	pw := pktline.NewWriter(w)

	caps := a.capabilityList()

	refs := a.Refs
	if a.SortRefs {
		refs = append([]Ref(nil), a.Refs...)
		sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	}

	if len(refs) == 0 {
		// Empty repository: advertise capabilities against the zero id on the
		// pseudo-ref "capabilities^{}".
		zero := ZeroIDFor(a.ObjectFormat)
		line := zero + " capabilities^{}\x00" + strings.Join(caps, " ") + "\n"
		if err := pw.WriteString(line); err != nil {
			return err
		}
		return pw.Flush()
	}

	for i, ref := range refs {
		line := ref.ID + " " + ref.Name
		if i == 0 {
			line += "\x00" + strings.Join(caps, " ")
		}
		line += "\n"
		if err := pw.WriteString(line); err != nil {
			return err
		}
		if ref.PeeledID != "" {
			if err := pw.WriteString(ref.PeeledID + " " + ref.Name + "^{}\n"); err != nil {
				return err
			}
		}
	}
	return pw.Flush()
}

// capabilityList returns the capability tokens to advertise, with symref
// capabilities for any symbolic refs appended (deduplicated against an existing
// symref entry for the same ref).
func (a RefAdvertisementV0) capabilityList() []string {
	caps := append([]string(nil), a.Capabilities...)
	for _, ref := range a.Refs {
		if ref.SymrefTarget != "" {
			caps = append(caps, "symref="+ref.Name+":"+ref.SymrefTarget)
		}
	}
	return caps
}
