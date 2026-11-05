package protocol

import (
	"strconv"
	"strings"
)

// decodeLsRefs decodes an ls-refs command frame.
func decodeLsRefs(f *v2Frame) *LsRefsRequest {
	req := &LsRefsRequest{Capabilities: f.Capabilities}
	for _, arg := range f.Args {
		keyword, rest, _ := strings.Cut(arg, " ")
		switch keyword {
		case "symrefs":
			req.Symrefs = true
		case "peel":
			req.Peel = true
		case "unborn":
			req.Unborn = true
		case "ref-prefix":
			req.RefPrefixes = append(req.RefPrefixes, rest)
		default:
			req.Unknown = append(req.Unknown, arg)
		}
	}
	return req
}

// decodeFetch decodes a fetch command frame. It validates object-ID lengths
// against the negotiated object-format.
func decodeFetch(f *v2Frame) (*FetchRequest, error) {
	const op = "fetch"
	req := &FetchRequest{Capabilities: f.Capabilities}
	hexLen := hexLenForFormat(f.Capabilities.ObjectFormat())

	for _, arg := range f.Args {
		keyword, rest, hasRest := strings.Cut(arg, " ")
		switch keyword {
		case "want":
			if err := checkOID(op, rest, hexLen); err != nil {
				return nil, err
			}
			req.Wants = append(req.Wants, rest)
		case "want-ref":
			req.WantRefs = append(req.WantRefs, rest)
		case "have":
			if err := checkOID(op, rest, hexLen); err != nil {
				return nil, err
			}
			req.Haves = append(req.Haves, rest)
		case "shallow":
			if err := checkOID(op, rest, hexLen); err != nil {
				return nil, err
			}
			req.Shallows = append(req.Shallows, rest)
		case "done":
			req.Done = true
		case "thin-pack":
			req.ThinPack = true
		case "no-progress":
			req.NoProgress = true
		case "include-tag":
			req.IncludeTag = true
		case "ofs-delta":
			req.OfsDelta = true
		case "sideband-all":
			req.SidebandAll = true
		case "wait-for-done":
			req.WaitForDone = true
		case "filter":
			req.Filter = rest
		case "deepen":
			n, perr := strconv.Atoi(strings.TrimSpace(rest))
			if perr != nil {
				return nil, decodeErr(op, "bad deepen value %q", rest)
			}
			req.Deepen = n
		case "deepen-relative":
			req.DeepenRelative = true
		case "deepen-since":
			secs, perr := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if perr != nil {
				return nil, decodeErr(op, "bad deepen-since value %q", rest)
			}
			req.DeepenSince = secs
		case "deepen-not":
			req.DeepenNot = append(req.DeepenNot, rest)
		case "packfile-uris":
			if hasRest {
				req.PackfileURIs = splitCSV(rest)
			}
		default:
			req.Unknown = append(req.Unknown, arg)
		}
	}
	return req, nil
}

// decodeObjectInfo decodes an object-info command frame.
func decodeObjectInfo(f *v2Frame) (*ObjectInfoRequest, error) {
	const op = "object-info"
	req := &ObjectInfoRequest{Capabilities: f.Capabilities}
	hexLen := hexLenForFormat(f.Capabilities.ObjectFormat())

	for _, arg := range f.Args {
		keyword, rest, _ := strings.Cut(arg, " ")
		switch keyword {
		case "size":
			req.Attributes = append(req.Attributes, "size")
		case "oid":
			if err := checkOID(op, rest, hexLen); err != nil {
				return nil, err
			}
			req.OIDs = append(req.OIDs, rest)
		default:
			req.Unknown = append(req.Unknown, arg)
		}
	}
	return req, nil
}

// checkOID validates that oid is a hex string of the expected length.
func checkOID(op, oid string, hexLen int) error {
	if len(oid) != hexLen {
		return decodeErr(op, "object id %q has length %d, want %d", oid, len(oid), hexLen)
	}
	for i := 0; i < len(oid); i++ {
		c := oid[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return decodeErr(op, "object id %q contains non-hex byte", oid)
		}
	}
	return nil
}

// splitCSV splits a comma-separated value list, trimming whitespace and
// dropping empty entries.
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
