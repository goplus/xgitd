package protocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// pkt builds a pkt-line-framed stream from a sequence of items. A string is a
// data packet; the sentinels flush, delim, and respEnd emit control packets.
type ctrl int

const (
	flush ctrl = iota
	delim
	respEnd
)

func pkt(items ...any) string {
	var b strings.Builder
	for _, it := range items {
		switch v := it.(type) {
		case string:
			b.WriteString(hexLen(len(v) + 4))
			b.WriteString(v)
		case ctrl:
			switch v {
			case flush:
				b.WriteString("0000")
			case delim:
				b.WriteString("0001")
			case respEnd:
				b.WriteString("0002")
			}
		default:
			panic("pkt: unsupported item type")
		}
	}
	return b.String()
}

func hexLen(n int) string {
	const hex = "0123456789abcdef"
	return string([]byte{
		hex[(n>>12)&0xf],
		hex[(n>>8)&0xf],
		hex[(n>>4)&0xf],
		hex[n&0xf],
	})
}

const (
	oidA = "1111111111111111111111111111111111111111"
	oidB = "2222222222222222222222222222222222222222"
	oidC = "3333333333333333333333333333333333333333"
	zero = "0000000000000000000000000000000000000000"
)

func TestDecodeUploadPackV0Clone(t *testing.T) {
	// A v0 clone: first want carries capabilities, then more wants, flush,
	// then done (no haves on a fresh clone).
	in := pkt(
		"want "+oidA+" multi_ack_detailed side-band-64k thin-pack ofs-delta agent=git/2.40.0\n",
		"want "+oidB+"\n",
		flush,
		"done\n",
	)
	req, err := DecodeUploadPackV0(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Wants) != 2 || req.Wants[0] != oidA || req.Wants[1] != oidB {
		t.Fatalf("wants = %v", req.Wants)
	}
	if !req.Done {
		t.Fatal("Done = false, want true")
	}
	if !req.Capabilities.Has("thin-pack") || !req.Capabilities.Has("side-band-64k") {
		t.Fatalf("caps = %v", req.Capabilities.Names())
	}
	if got := req.Capabilities.Agent(); got != "git/2.40.0" {
		t.Fatalf("agent = %q", got)
	}
}

func TestDecodeUploadPackV0IncrementalFetch(t *testing.T) {
	// A want section, flush, then haves without done (client expects another
	// round).
	in := pkt(
		"want "+oidA+" ofs-delta\n",
		flush,
		"have "+oidB+"\n",
		"have "+oidC+"\n",
		flush,
	)
	req, err := DecodeUploadPackV0(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Haves) != 2 {
		t.Fatalf("haves = %v", req.Haves)
	}
	if req.Done {
		t.Fatal("Done = true, want false")
	}
}

func TestDecodeUploadPackV0ShallowDeepenFilter(t *testing.T) {
	in := pkt(
		"want "+oidA+" filter\n",
		"shallow "+oidB+"\n",
		"deepen 5\n",
		"deepen-since 1700000000\n",
		"deepen-not refs/heads/old\n",
		"filter blob:none\n",
		flush,
		"done\n",
	)
	req, err := DecodeUploadPackV0(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Shallows) != 1 || req.Shallows[0] != oidB {
		t.Fatalf("shallows = %v", req.Shallows)
	}
	if req.Deepen != 5 {
		t.Fatalf("deepen = %d", req.Deepen)
	}
	if req.DeepenSince != 1700000000 {
		t.Fatalf("deepen-since = %d", req.DeepenSince)
	}
	if len(req.DeepenNot) != 1 || req.DeepenNot[0] != "refs/heads/old" {
		t.Fatalf("deepen-not = %v", req.DeepenNot)
	}
	if req.Filter != "blob:none" {
		t.Fatalf("filter = %q", req.Filter)
	}
}

func TestDecodeUploadPackV0Unknown(t *testing.T) {
	in := pkt(
		"want "+oidA+"\n",
		"brand-new-thing argument\n",
		flush,
		"done\n",
	)
	req, err := DecodeUploadPackV0(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Unknown) != 1 || req.Unknown[0] != "brand-new-thing argument" {
		t.Fatalf("unknown = %v", req.Unknown)
	}
}

func TestDecodeUploadPackV0Empty(t *testing.T) {
	// A flush-only probe decodes to an empty request without error.
	req, err := DecodeUploadPackV0(strings.NewReader(pkt(flush)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Wants) != 0 {
		t.Fatalf("wants = %v", req.Wants)
	}
}

func TestDecodeUploadPackV0CleanEmptyEOF(t *testing.T) {
	// EOF with nothing sent at all is a clean no-op, not an error.
	req, err := DecodeUploadPackV0(strings.NewReader(""))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Wants) != 0 {
		t.Fatalf("wants = %v", req.Wants)
	}
}

func TestDecodeUploadPackV0TruncatedWantSection(t *testing.T) {
	// A want line with no terminating flush before EOF is a truncated
	// negotiation and must be reported as an error, not silently accepted.
	in := pkt("want " + oidA + "\n") // no flush
	_, err := DecodeUploadPackV0(strings.NewReader(in))
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v, want ErrUnexpectedEOF", err)
	}
}

func TestDecodeReceivePackV0WithReportStatus(t *testing.T) {
	// Push creating one ref, updating another. Capabilities after NUL on the
	// first command. Packfile follows the flush.
	packData := "PACK\x00\x00\x00\x02rest-of-pack"
	in := pkt(
		zero+" "+oidA+" refs/heads/new\x00report-status side-band-64k agent=git/2.40.0\n",
		oidB+" "+oidC+" refs/heads/main\n",
		flush,
	) + packData

	req, err := DecodeReceivePackV0(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Commands) != 2 {
		t.Fatalf("commands = %v", req.Commands)
	}
	if req.Commands[0].OldID != zero || req.Commands[0].NewID != oidA || req.Commands[0].Ref != "refs/heads/new" {
		t.Fatalf("command[0] = %+v", req.Commands[0])
	}
	if req.Commands[1].Ref != "refs/heads/main" {
		t.Fatalf("command[1] = %+v", req.Commands[1])
	}
	if !req.Capabilities.Has("report-status") {
		t.Fatalf("caps = %v", req.Capabilities.Names())
	}
	if req.Packfile == nil {
		t.Fatal("packfile = nil, want reader")
	}
	got, _ := readAll(req.Packfile)
	if string(got) != packData {
		t.Fatalf("packfile = %q, want %q", got, packData)
	}
}

func TestDecodeReceivePackV0PushOptions(t *testing.T) {
	in := pkt(
		oidA+" "+oidB+" refs/heads/main\x00report-status push-options\n",
		flush,
		"ci.skip\n",
		"notify=team\n",
		flush,
	) + "PACKdata"

	req, err := DecodeReceivePackV0(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.PushOptions) != 2 || req.PushOptions[0] != "ci.skip" || req.PushOptions[1] != "notify=team" {
		t.Fatalf("push-options = %v", req.PushOptions)
	}
	if req.Packfile == nil {
		t.Fatal("packfile = nil")
	}
}

func TestDecodeReceivePackV0DeleteOnlyNoPack(t *testing.T) {
	// Delete-only push sends no packfile.
	in := pkt(
		oidA+" "+zero+" refs/heads/gone\x00report-status\n",
		flush,
	)
	req, err := DecodeReceivePackV0(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Packfile != nil {
		t.Fatal("packfile != nil, want nil for delete-only push")
	}
}

func TestDecodeV2LsRefs(t *testing.T) {
	in := pkt(
		"command=ls-refs\n",
		"agent=git/2.40.0\n",
		"object-format=sha1\n",
		delim,
		"peel\n",
		"symrefs\n",
		"unborn\n",
		"ref-prefix refs/heads/\n",
		"ref-prefix refs/tags/\n",
		flush,
	)
	cmd, req, err := DecodeV2(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd != "ls-refs" {
		t.Fatalf("cmd = %q", cmd)
	}
	ls, ok := req.(*LsRefsRequest)
	if !ok {
		t.Fatalf("req type = %T", req)
	}
	if !ls.Peel || !ls.Symrefs || !ls.Unborn {
		t.Fatalf("flags: peel=%v symrefs=%v unborn=%v", ls.Peel, ls.Symrefs, ls.Unborn)
	}
	if len(ls.RefPrefixes) != 2 {
		t.Fatalf("ref-prefixes = %v", ls.RefPrefixes)
	}
	if ls.Capabilities.Agent() != "git/2.40.0" {
		t.Fatalf("agent = %q", ls.Capabilities.Agent())
	}
}

func TestDecodeV2Fetch(t *testing.T) {
	in := pkt(
		"command=fetch\n",
		"agent=git/2.40.0\n",
		"object-format=sha1\n",
		delim,
		"thin-pack\n",
		"ofs-delta\n",
		"no-progress\n",
		"include-tag\n",
		"want "+oidA+"\n",
		"want "+oidB+"\n",
		"have "+oidC+"\n",
		"want-ref refs/heads/main\n",
		"shallow "+oidA+"\n",
		"deepen 3\n",
		"deepen-relative\n",
		"deepen-since 1700000000\n",
		"deepen-not refs/heads/old\n",
		"filter blob:limit=1024\n",
		"sideband-all\n",
		"packfile-uris http,https\n",
		"wait-for-done\n",
		"done\n",
		flush,
	)
	cmd, req, err := DecodeV2(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd != "fetch" {
		t.Fatalf("cmd = %q", cmd)
	}
	f := req.(*FetchRequest)
	if len(f.Wants) != 2 {
		t.Fatalf("wants = %v", f.Wants)
	}
	if len(f.Haves) != 1 {
		t.Fatalf("haves = %v", f.Haves)
	}
	if !f.ThinPack || !f.OfsDelta || !f.NoProgress || !f.IncludeTag || !f.SidebandAll || !f.WaitForDone || !f.Done || !f.DeepenRelative {
		t.Fatalf("flags: %+v", f)
	}
	if f.Deepen != 3 || f.DeepenSince != 1700000000 {
		t.Fatalf("deepen=%d since=%d", f.Deepen, f.DeepenSince)
	}
	if len(f.DeepenNot) != 1 || len(f.WantRefs) != 1 || len(f.Shallows) != 1 {
		t.Fatalf("deepen-not=%v want-ref=%v shallow=%v", f.DeepenNot, f.WantRefs, f.Shallows)
	}
	if f.Filter != "blob:limit=1024" {
		t.Fatalf("filter = %q", f.Filter)
	}
	if len(f.PackfileURIs) != 2 || f.PackfileURIs[0] != "http" {
		t.Fatalf("packfile-uris = %v", f.PackfileURIs)
	}
}

func TestDecodeV2FetchSHA256(t *testing.T) {
	oid256 := strings.Repeat("a", 64)
	in := pkt(
		"command=fetch\n",
		"object-format=sha256\n",
		delim,
		"want "+oid256+"\n",
		"done\n",
		flush,
	)
	_, req, err := DecodeV2(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	f := req.(*FetchRequest)
	if len(f.Wants) != 1 || f.Wants[0] != oid256 {
		t.Fatalf("wants = %v", f.Wants)
	}
}

func TestDecodeV2FetchBadOIDLength(t *testing.T) {
	// A short oid under sha1 is rejected.
	in := pkt(
		"command=fetch\n",
		"object-format=sha1\n",
		delim,
		"want deadbeef\n",
		"done\n",
		flush,
	)
	_, _, err := DecodeV2(strings.NewReader(in))
	if err == nil {
		t.Fatal("expected error for short oid")
	}
	var de *DecodeError
	if !asDecodeError(err, &de) {
		t.Fatalf("err type = %T", err)
	}
}

func TestDecodeV2ObjectInfo(t *testing.T) {
	in := pkt(
		"command=object-info\n",
		"object-format=sha1\n",
		delim,
		"size\n",
		"oid "+oidA+"\n",
		"oid "+oidB+"\n",
		flush,
	)
	cmd, req, err := DecodeV2(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cmd != "object-info" {
		t.Fatalf("cmd = %q", cmd)
	}
	oi := req.(*ObjectInfoRequest)
	if len(oi.Attributes) != 1 || oi.Attributes[0] != "size" {
		t.Fatalf("attributes = %v", oi.Attributes)
	}
	if len(oi.OIDs) != 2 {
		t.Fatalf("oids = %v", oi.OIDs)
	}
}

func TestDecodeV2UnknownArg(t *testing.T) {
	in := pkt(
		"command=ls-refs\n",
		delim,
		"brand-new-flag\n",
		flush,
	)
	_, req, err := DecodeV2(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	ls := req.(*LsRefsRequest)
	if len(ls.Unknown) != 1 || ls.Unknown[0] != "brand-new-flag" {
		t.Fatalf("unknown = %v", ls.Unknown)
	}
}

func TestDecodeV2UnknownCommand(t *testing.T) {
	in := pkt("command=bundle-uri\n", flush)
	_, _, err := DecodeV2(strings.NewReader(in))
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestCapabilityAdvertisementDefault(t *testing.T) {
	var buf bytes.Buffer
	if err := DefaultCapabilityAdvertisement().WriteV2(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"version 2", "ls-refs", "fetch=", "object-info", "object-format=sha1", "agent="} {
		if !strings.Contains(out, want) {
			t.Fatalf("advertisement missing %q:\n%s", want, out)
		}
	}
	// Must end with a flush.
	if !strings.HasSuffix(out, "0000") {
		t.Fatalf("advertisement does not end with flush:\n%s", out)
	}
}

func TestCapabilityAdvertisementStartsWithVersionLine(t *testing.T) {
	// Per the protocol-v2 grammar the advertisement must begin with a
	// "version 2" pkt-line; git rejects it otherwise.
	var buf bytes.Buffer
	if err := DefaultCapabilityAdvertisement().WriteV2(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	// "version 2\n" is 10 bytes; with the 4-byte length prefix the first
	// pkt-line is "000eversion 2\n".
	if got := buf.String(); !strings.HasPrefix(got, "000eversion 2\n") {
		t.Fatalf("advertisement does not start with version line:\n%q", got)
	}
}

// helpers -------------------------------------------------------------------

func readAll(r Packfile) ([]byte, error) {
	var buf bytes.Buffer
	p := make([]byte, 512)
	for {
		n, err := r.Read(p)
		buf.Write(p[:n])
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}

func asDecodeError(err error, target **DecodeError) bool {
	return errors.As(err, target)
}
