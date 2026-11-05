package protocol

import (
	"bytes"
	"strings"
	"testing"

	"github.com/goplus/xgitd/internal/pktline"
)

// collectPackets reads every pkt-line from s and returns them as a slice where
// data packets are their payload string and control packets are "FLUSH",
// "DELIM", or "RESPONSE-END".
func collectPackets(t *testing.T, s string) []string {
	t.Helper()
	r := pktline.NewReader(strings.NewReader(s))
	var out []string
	for {
		p, err := r.ReadPacket()
		if err != nil {
			// io.EOF ends the stream; anything else is unexpected in these
			// round-trip tests. Raw (non-pkt) trailing bytes would surface here,
			// so callers that write raw data must not use this helper past that
			// point.
			break
		}
		switch p.Kind {
		case pktline.KindFlush:
			out = append(out, "FLUSH")
		case pktline.KindDelim:
			out = append(out, "DELIM")
		case pktline.KindResponseEnd:
			out = append(out, "RESPONSE-END")
		default:
			out = append(out, string(p.Data))
		}
	}
	return out
}

func TestRefAdvertisementV0NonEmpty(t *testing.T) {
	adv := RefAdvertisementV0{
		Service: "git-upload-pack",
		Refs: []Ref{
			{Name: "HEAD", ID: oidA, SymrefTarget: "refs/heads/main"},
			{Name: "refs/heads/main", ID: oidA},
			{Name: "refs/tags/v1", ID: oidB, PeeledID: oidC},
		},
		Capabilities: []string{"side-band-64k", "ofs-delta", "agent=xgitd"},
	}
	var buf bytes.Buffer
	if err := adv.WriteV0(&buf); err != nil {
		t.Fatalf("WriteV0: %v", err)
	}
	pkts := collectPackets(t, buf.String())

	// First line: HEAD with caps after NUL, including the auto symref cap.
	if len(pkts) < 1 {
		t.Fatalf("no packets")
	}
	first := pkts[0]
	oidAndRef, caps, ok := strings.Cut(strings.TrimRight(first, "\n"), "\x00")
	if !ok {
		t.Fatalf("first line has no NUL: %q", first)
	}
	if oidAndRef != oidA+" HEAD" {
		t.Fatalf("first ref = %q", oidAndRef)
	}
	if !strings.Contains(caps, "side-band-64k") || !strings.Contains(caps, "symref=HEAD:refs/heads/main") {
		t.Fatalf("caps = %q", caps)
	}
	// Peeled tag must emit an extra ^{} line.
	joined := strings.Join(pkts, "|")
	if !strings.Contains(joined, oidC+" refs/tags/v1^{}\n") {
		t.Fatalf("missing peeled line in %q", joined)
	}
	if pkts[len(pkts)-1] != "FLUSH" {
		t.Fatalf("advertisement not flush-terminated: %v", pkts)
	}
}

func TestRefAdvertisementV0Empty(t *testing.T) {
	adv := RefAdvertisementV0{
		Service:      "git-upload-pack",
		Capabilities: []string{"side-band-64k", "agent=xgitd"},
	}
	var buf bytes.Buffer
	if err := adv.WriteV0(&buf); err != nil {
		t.Fatalf("WriteV0: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	if len(pkts) != 2 {
		t.Fatalf("expected capabilities line + flush, got %v", pkts)
	}
	if !strings.HasPrefix(pkts[0], ZeroID+" capabilities^{}\x00") {
		t.Fatalf("empty advertisement line = %q", pkts[0])
	}
	if pkts[1] != "FLUSH" {
		t.Fatalf("not flush-terminated: %v", pkts)
	}
}

func TestRefAdvertisementV0Sorted(t *testing.T) {
	adv := RefAdvertisementV0{
		Refs: []Ref{
			{Name: "refs/heads/z", ID: oidA},
			{Name: "refs/heads/a", ID: oidB},
		},
		SortRefs: true,
	}
	var buf bytes.Buffer
	if err := adv.WriteV0(&buf); err != nil {
		t.Fatalf("WriteV0: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	// First ref (carrying caps) must be the alphabetically-first name.
	if !strings.HasPrefix(pkts[0], oidB+" refs/heads/a") {
		t.Fatalf("not sorted: first line %q", pkts[0])
	}
}

func TestUploadPackResponderNoSideband(t *testing.T) {
	var buf bytes.Buffer
	r := NewUploadPackResponder(&buf, false)
	if err := r.NAK(); err != nil {
		t.Fatal(err)
	}
	w, finish := r.PackWriter()
	if _, err := w.Write([]byte("PACKDATA")); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}

	// The NAK is a pkt-line; the pack bytes follow raw.
	s := buf.String()
	if !strings.HasPrefix(s, "0008NAK\n") {
		t.Fatalf("expected NAK pkt-line prefix, got %q", s)
	}
	if !strings.HasSuffix(s, "PACKDATA") {
		t.Fatalf("expected raw pack bytes suffix, got %q", s)
	}
}

func TestUploadPackResponderSideband(t *testing.T) {
	var buf bytes.Buffer
	r := NewUploadPackResponder(&buf, true)
	if err := r.ACK(oidA); err != nil {
		t.Fatal(err)
	}
	if err := r.Progress("counting objects\n"); err != nil {
		t.Fatal(err)
	}
	w, finish := r.PackWriter()
	if _, err := w.Write([]byte("PACKDATA")); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}

	pkts := collectPackets(t, buf.String())
	if pkts[0] != "ACK "+oidA+"\n" {
		t.Fatalf("first packet = %q", pkts[0])
	}
	// Progress on channel 2, pack on channel 1.
	var sawProgress, sawData bool
	for _, p := range pkts[1:] {
		if p == "FLUSH" {
			continue
		}
		switch p[0] {
		case pktline.SideBandProgress:
			sawProgress = true
			if string(p[1:]) != "counting objects\n" {
				t.Fatalf("progress payload = %q", p[1:])
			}
		case pktline.SideBandData:
			sawData = true
			if string(p[1:]) != "PACKDATA" {
				t.Fatalf("data payload = %q", p[1:])
			}
		}
	}
	if !sawProgress || !sawData {
		t.Fatalf("progress=%v data=%v", sawProgress, sawData)
	}
	if pkts[len(pkts)-1] != "FLUSH" {
		t.Fatalf("not flush-terminated: %v", pkts)
	}
}

func TestReportStatusSuccess(t *testing.T) {
	rs := ReportStatus{
		UnpackOK: true,
		Refs: []RefStatus{
			{Ref: "refs/heads/main", OK: true},
			{Ref: "refs/heads/bad", OK: false, Reason: "non-fast-forward"},
		},
	}
	var buf bytes.Buffer
	if err := rs.WriteV0(&buf, false); err != nil {
		t.Fatalf("WriteV0: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	want := []string{"unpack ok\n", "ok refs/heads/main\n", "ng refs/heads/bad non-fast-forward\n", "FLUSH"}
	if strings.Join(pkts, "|") != strings.Join(want, "|") {
		t.Fatalf("report = %v, want %v", pkts, want)
	}
}

func TestReportStatusUnpackFailure(t *testing.T) {
	rs := ReportStatus{UnpackOK: false, UnpackError: "index-pack failed"}
	var buf bytes.Buffer
	if err := rs.WriteV0(&buf, false); err != nil {
		t.Fatalf("WriteV0: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	if pkts[0] != "unpack index-pack failed\n" {
		t.Fatalf("unpack line = %q", pkts[0])
	}
}

func TestReportStatusSideband(t *testing.T) {
	rs := ReportStatus{UnpackOK: true, Refs: []RefStatus{{Ref: "refs/heads/main", OK: true}}}
	var buf bytes.Buffer
	if err := rs.WriteV0(&buf, true); err != nil {
		t.Fatalf("WriteV0: %v", err)
	}
	// The report is multiplexed on channel 1; reassemble it.
	pkts := collectPackets(t, buf.String())
	var inner bytes.Buffer
	for _, p := range pkts {
		if p == "FLUSH" {
			continue
		}
		if p[0] != pktline.SideBandData {
			t.Fatalf("unexpected channel %d", p[0])
		}
		inner.WriteString(p[1:])
	}
	innerPkts := collectPackets(t, inner.String())
	if innerPkts[0] != "unpack ok\n" || innerPkts[1] != "ok refs/heads/main\n" {
		t.Fatalf("inner report = %v", innerPkts)
	}
}

func TestWriteLsRefs(t *testing.T) {
	req := &LsRefsRequest{Symrefs: true, Peel: true}
	refs := []Ref{
		{Name: "HEAD", ID: oidA, SymrefTarget: "refs/heads/main"},
		{Name: "refs/heads/main", ID: oidA},
		{Name: "refs/tags/v1", ID: oidB, PeeledID: oidC},
	}
	var buf bytes.Buffer
	if err := WriteLsRefs(&buf, req, refs); err != nil {
		t.Fatalf("WriteLsRefs: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	want := []string{
		oidA + " HEAD symref-target:refs/heads/main\n",
		oidA + " refs/heads/main\n",
		oidB + " refs/tags/v1 peeled:" + oidC + "\n",
		"FLUSH",
	}
	if strings.Join(pkts, "|") != strings.Join(want, "|") {
		t.Fatalf("ls-refs = %v, want %v", pkts, want)
	}
}

func TestWriteLsRefsNoAttributesWhenNotRequested(t *testing.T) {
	req := &LsRefsRequest{} // no symrefs, no peel
	refs := []Ref{{Name: "HEAD", ID: oidA, SymrefTarget: "refs/heads/main", PeeledID: oidC}}
	var buf bytes.Buffer
	if err := WriteLsRefs(&buf, req, refs); err != nil {
		t.Fatalf("WriteLsRefs: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	if pkts[0] != oidA+" HEAD\n" {
		t.Fatalf("attributes leaked when not requested: %q", pkts[0])
	}
}

func TestWriteLsRefsUnborn(t *testing.T) {
	req := &LsRefsRequest{Symrefs: true, Unborn: true}
	refs := []Ref{{Name: "HEAD", Unborn: true, SymrefTarget: "refs/heads/main"}}
	var buf bytes.Buffer
	if err := WriteLsRefs(&buf, req, refs); err != nil {
		t.Fatalf("WriteLsRefs: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	if pkts[0] != "unborn HEAD symref-target:refs/heads/main\n" {
		t.Fatalf("unborn line = %q", pkts[0])
	}
}

func TestFetchResponderPackOnly(t *testing.T) {
	var buf bytes.Buffer
	r := NewFetchResponder(&buf)
	w, finish, err := r.PackSection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("PACKDATA")); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}

	pkts := collectPackets(t, buf.String())
	if pkts[0] != "packfile\n" {
		t.Fatalf("first packet = %q", pkts[0])
	}
	// pack data on channel 1
	if pkts[1][0] != pktline.SideBandData || string(pkts[1][1:]) != "PACKDATA" {
		t.Fatalf("pack packet = %q", pkts[1])
	}
	if pkts[len(pkts)-1] != "FLUSH" {
		t.Fatalf("not flush-terminated: %v", pkts)
	}
}

func TestFetchResponderSections(t *testing.T) {
	var buf bytes.Buffer
	r := NewFetchResponder(&buf)
	if err := r.Acknowledgments([]ObjectID{oidA}, true); err != nil {
		t.Fatal(err)
	}
	if err := r.ShallowInfo([]ObjectID{oidB}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.WantedRefs([]WantedRef{{Name: "refs/heads/main", ID: oidA}}); err != nil {
		t.Fatal(err)
	}
	w, finish, err := r.PackSection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("PK")); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}

	pkts := collectPackets(t, buf.String())
	joined := strings.Join(pkts, "|")
	// Sections must be separated by DELIM and appear in order.
	for _, section := range []string{"acknowledgments\n", "shallow-info\n", "wanted-refs\n", "packfile\n"} {
		if !strings.Contains(joined, section) {
			t.Fatalf("missing section %q in %q", section, joined)
		}
	}
	if strings.Count(joined, "DELIM") != 3 {
		t.Fatalf("expected 3 delimiters between 4 sections, got %q", joined)
	}
	if !strings.Contains(joined, "ACK "+oidA+"\n") || !strings.Contains(joined, "ready\n") {
		t.Fatalf("acknowledgments content missing: %q", joined)
	}
	if !strings.Contains(joined, "shallow "+oidB+"\n") {
		t.Fatalf("shallow-info content missing: %q", joined)
	}
	if !strings.Contains(joined, oidA+" refs/heads/main\n") {
		t.Fatalf("wanted-refs content missing: %q", joined)
	}
}

func TestFetchResponderNAK(t *testing.T) {
	var buf bytes.Buffer
	r := NewFetchResponder(&buf)
	if err := r.Acknowledgments(nil, false); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	pkts := collectPackets(t, buf.String())
	if pkts[0] != "acknowledgments\n" || pkts[1] != "NAK\n" {
		t.Fatalf("NAK response = %v", pkts)
	}
	if pkts[len(pkts)-1] != "FLUSH" {
		t.Fatalf("not flush-terminated: %v", pkts)
	}
}

func TestFetchResponderNoReadyRejectsPackfile(t *testing.T) {
	// A non-ready acknowledgments section is negotiation-only; attempting a
	// packfile (or shallow-info / wanted-refs) afterward must be refused so the
	// handler cannot emit a stream git would reject.
	for name, cont := range map[string]func(*FetchResponder) error{
		"packfile":     func(r *FetchResponder) error { _, _, err := r.PackSection(); return err },
		"shallow-info": func(r *FetchResponder) error { return r.ShallowInfo([]ObjectID{oidA}, nil) },
		"wanted-refs":  func(r *FetchResponder) error { return r.WantedRefs([]WantedRef{{Name: "x", ID: oidA}}) },
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			r := NewFetchResponder(&buf)
			if err := r.Acknowledgments([]ObjectID{oidA}, false); err != nil {
				t.Fatal(err)
			}
			if err := cont(r); err != ErrNegotiationIncomplete {
				t.Fatalf("continuation err = %v, want ErrNegotiationIncomplete", err)
			}
		})
	}
}

func TestFetchResponderReadyAllowsPackfile(t *testing.T) {
	// A ready acknowledgments section may be followed by a packfile.
	var buf bytes.Buffer
	r := NewFetchResponder(&buf)
	if err := r.Acknowledgments([]ObjectID{oidA}, true); err != nil {
		t.Fatal(err)
	}
	w, finish, err := r.PackSection()
	if err != nil {
		t.Fatalf("PackSection after ready: %v", err)
	}
	if _, err := w.Write([]byte("PK")); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(collectPackets(t, buf.String()), "|")
	if !strings.Contains(joined, "ready\n") || !strings.Contains(joined, "packfile\n") {
		t.Fatalf("expected ready + packfile, got %q", joined)
	}
}

func TestWriteObjectInfoAlwaysWritesHeader(t *testing.T) {
	// Even with no requested attributes the response must begin with the
	// (empty) attribute header line per the v2 grammar.
	req := &ObjectInfoRequest{}
	var buf bytes.Buffer
	if err := WriteObjectInfo(&buf, req, []ObjectInfoEntry{{OID: oidA}}); err != nil {
		t.Fatalf("WriteObjectInfo: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	if pkts[0] != "\n" {
		t.Fatalf("first line = %q, want empty header line", pkts[0])
	}
	if pkts[1] != oidA+"\n" {
		t.Fatalf("object line = %q", pkts[1])
	}
}

func TestWriteObjectInfo(t *testing.T) {
	req := &ObjectInfoRequest{Attributes: []string{"size"}}
	entries := []ObjectInfoEntry{
		{OID: oidA, Size: 42},
		{OID: oidB, Size: -1}, // missing
	}
	var buf bytes.Buffer
	if err := WriteObjectInfo(&buf, req, entries); err != nil {
		t.Fatalf("WriteObjectInfo: %v", err)
	}
	pkts := collectPackets(t, buf.String())
	want := []string{"size\n", oidA + " 42\n", oidB + "\n", "FLUSH"}
	if strings.Join(pkts, "|") != strings.Join(want, "|") {
		t.Fatalf("object-info = %v, want %v", pkts, want)
	}
}

func TestZeroIDFor(t *testing.T) {
	if got := ZeroIDFor(ObjectFormatSHA1); len(got) != 40 {
		t.Fatalf("sha1 zero id length = %d", len(got))
	}
	if got := ZeroIDFor(ObjectFormatSHA256); len(got) != 64 {
		t.Fatalf("sha256 zero id length = %d", len(got))
	}
	if got := ZeroIDFor(""); got != ZeroID {
		t.Fatalf("default zero id = %q", got)
	}
}
