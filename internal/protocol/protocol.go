// Package protocol decodes Git wire-protocol requests into typed Go values and
// encodes the matching responses.
//
// It handles protocol version negotiation (v0 and v2), capability parsing, the
// request decoders, and the response writers:
//
//   - v0: git-upload-pack (fetch/clone) and git-receive-pack (push).
//   - v2: ls-refs, fetch, and object-info.
//
// The decoders never touch object storage or packfiles. A v0 push's packfile is
// exposed as an [io.Reader] positioned at the start of the pack data; the
// decoder does not read it.
//
// The response writers frame a handler's output correctly on the wire (the v0
// ref advertisement, upload-pack ACK/NAK negotiation and side-band packfile
// section, receive-pack report-status, and the v2 ls-refs/fetch/object-info
// sections) while leaving the content — which refs exist, which objects to send,
// the packfile bytes themselves — entirely to the handler.
package protocol

// Version numbers used on the wire. v1 is treated as v0 with a leading version
// line (it has no behavioral difference here), so only 0 and 2 are modeled.
const (
	// VersionV0 is protocol version 0 (and, for our purposes, v1).
	VersionV0 = 0
	// VersionV2 is protocol version 2.
	VersionV2 = 2
)

// Object-format identifiers, as sent in the object-format capability.
const (
	ObjectFormatSHA1   = "sha1"
	ObjectFormatSHA256 = "sha256"
)

// Hex object-ID lengths for the supported hash algorithms.
const (
	sha1HexLen   = 40
	sha256HexLen = 64
)

// hexLenForFormat returns the hex object-ID length for an object-format name.
// An empty or unknown name defaults to SHA-1.
func hexLenForFormat(format string) int {
	if format == ObjectFormatSHA256 {
		return sha256HexLen
	}
	return sha1HexLen
}
