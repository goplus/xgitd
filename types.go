package xgitd

import "github.com/goplus/xgitd/protocol"

// The decoded request types live in the protocol package so the decoders and
// the Handler interface can share them without an import cycle. They are
// re-exported here as aliases so handler implementations only need to import
// this package.
type (
	// UploadPackRequest is a decoded v0 fetch/clone negotiation.
	UploadPackRequest = protocol.UploadPackRequest
	// ReceivePackRequest is a decoded v0 push.
	ReceivePackRequest = protocol.ReceivePackRequest
	// LsRefsRequest is a decoded v2 ls-refs command.
	LsRefsRequest = protocol.LsRefsRequest
	// FetchRequest is a decoded v2 fetch command.
	FetchRequest = protocol.FetchRequest
	// ObjectInfoRequest is a decoded v2 object-info command.
	ObjectInfoRequest = protocol.ObjectInfoRequest

	// RefUpdate is one ref-update command in a push.
	RefUpdate = protocol.RefUpdate
	// Capabilities is a parsed capability set.
	Capabilities = protocol.Capabilities
)
