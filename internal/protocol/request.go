package protocol

import "io"

// ObjectID is a hex-encoded object identifier, as it appears on the wire (40
// hex chars for SHA-1, 64 for SHA-256). It is kept as a string; the decoders
// validate its length against the negotiated object-format but do not otherwise
// interpret it.
type ObjectID = string

// UploadPackRequest is a decoded v0 fetch/clone negotiation
// (git-upload-pack). Over stateless HTTP a negotiation may span several POSTs;
// each POST is decoded and delivered as its own UploadPackRequest.
type UploadPackRequest struct {
	// Capabilities are the capabilities that rode along on the first "want"
	// line (side-band-64k, ofs-delta, thin-pack, multi_ack_detailed, no-done,
	// include-tag, agent=..., and so on).
	Capabilities *Capabilities
	// Wants are the wanted object IDs.
	Wants []ObjectID
	// Shallows are the client's existing shallow boundary ("shallow <oid>").
	Shallows []ObjectID
	// Haves are the object IDs the client already has ("have <oid>").
	Haves []ObjectID

	// Deepen is the "deepen <n>" depth, or 0 if absent.
	Deepen int
	// DeepenSince is the "deepen-since <unix-time>" value, or 0 if absent.
	DeepenSince int64
	// DeepenNot holds any "deepen-not <ref>" lines.
	DeepenNot []string
	// Filter is the "filter <spec>" blob/tree filter, or "" if absent.
	Filter string

	// Done reports whether the client sent a "done" line, ending negotiation.
	Done bool

	// Unknown collects any request lines the decoder did not model, so a newer
	// client sending something unexpected is observable rather than silently
	// dropped.
	Unknown []string
}

// ReceivePackRequest is a decoded v0 push (git-receive-pack).
type ReceivePackRequest struct {
	// Capabilities are the capabilities from the first command line
	// (report-status, atomic, push-options, side-band-64k, agent=...).
	Capabilities *Capabilities
	// Commands are the ref-update commands, in the order the client sent them.
	Commands []RefUpdate
	// PushOptions holds the push options, if the client negotiated
	// "push-options" and sent any.
	PushOptions []string
	// Packfile is positioned at the start of the pack data that follows the
	// command list. It is nil when the push updated only deletions and the
	// client sent no pack. The server does not read it; the handler decides
	// whether to consume it.
	Packfile io.Reader

	// Unknown collects unmodeled command-section lines.
	Unknown []string
}

// RefUpdate is one ref-update command in a push: change OldID to NewID for Ref.
// A zero OldID creates a ref; a zero NewID deletes it.
type RefUpdate struct {
	OldID ObjectID
	NewID ObjectID
	Ref   string
}

// LsRefsRequest is a decoded v2 ls-refs command.
type LsRefsRequest struct {
	// Capabilities are the per-command capability lines (agent=,
	// object-format=).
	Capabilities *Capabilities
	// Symrefs requests that symbolic refs be reported with their targets.
	Symrefs bool
	// Peel requests that annotated tags be peeled to the object they point to.
	Peel bool
	// Unborn requests that an unborn HEAD be reported.
	Unborn bool
	// RefPrefixes limits the advertisement to refs under these prefixes.
	RefPrefixes []string

	// Unknown collects unmodeled argument lines.
	Unknown []string
}

// FetchRequest is a decoded v2 fetch command.
type FetchRequest struct {
	// Capabilities are the per-command capability lines.
	Capabilities *Capabilities

	Wants    []ObjectID // "want <oid>"
	WantRefs []string   // "want-ref <ref>"
	Haves    []ObjectID // "have <oid>"
	Shallows []ObjectID // "shallow <oid>"

	Done        bool   // "done"
	ThinPack    bool   // "thin-pack"
	NoProgress  bool   // "no-progress"
	IncludeTag  bool   // "include-tag"
	OfsDelta    bool   // "ofs-delta"
	SidebandAll bool   // "sideband-all"
	WaitForDone bool   // "wait-for-done"
	Filter      string // "filter <spec>"

	Deepen         int      // "deepen <n>"
	DeepenRelative bool     // "deepen-relative"
	DeepenSince    int64    // "deepen-since <unix-time>"
	DeepenNot      []string // "deepen-not <ref>"

	// PackfileURIs holds the protocols listed in a "packfile-uris <p1,p2,...>"
	// line.
	PackfileURIs []string

	// Unknown collects unmodeled argument lines.
	Unknown []string
}

// ObjectInfoRequest is a decoded v2 object-info command.
type ObjectInfoRequest struct {
	// Capabilities are the per-command capability lines.
	Capabilities *Capabilities
	// Attributes are the requested object attributes, e.g. "size".
	Attributes []string
	// OIDs are the requested object IDs.
	OIDs []ObjectID

	// Unknown collects unmodeled argument lines.
	Unknown []string
}
