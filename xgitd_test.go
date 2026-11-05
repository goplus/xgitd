package xgitd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeRepoPath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"org/repo.git", "org/repo.git", false},
		{"/org/repo.git", "org/repo.git", false},
		{"//org//repo.git", "org/repo.git", false},
		{"git@host:org/repo.git", "org/repo.git", false},
		{"host:org/repo.git", "org/repo.git", false},
		{"org/repo.git/", "org/repo.git", false},
		{"  org/repo.git  ", "org/repo.git", false},
		{"deep/nested/repo.git", "deep/nested/repo.git", false},
		// Unsafe / invalid.
		{"", "", true},
		{"/", "", true},
		{"..", "", true},
		{"../etc/passwd", "", true},
		{"org/../../etc", "", true},
		{"org/./repo.git", "", true},
		{"org/repo\x00.git", "", true},
		{`..\..\etc`, "", true},
		{`org\repo.git`, "", true},
	}
	for _, tt := range tests {
		got, err := NormalizeRepoPath(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NormalizeRepoPath(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeRepoPath(%q) error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeRepoPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNopHandlerReturnsNotImplemented(t *testing.T) {
	var h NopHandler
	ctx := context.Background()
	s := &Session{}
	errs := []error{
		h.AdvertiseRefs(ctx, s),
		h.UploadPack(ctx, s, &UploadPackRequest{}),
		h.ReceivePack(ctx, s, &ReceivePackRequest{}),
		h.LsRefs(ctx, s, &LsRefsRequest{}),
		h.Fetch(ctx, s, &FetchRequest{}),
		h.ObjectInfo(ctx, s, &ObjectInfoRequest{}),
	}
	for i, err := range errs {
		if !errors.Is(err, ErrNotImplemented) {
			t.Errorf("method %d: err = %v, want ErrNotImplemented", i, err)
		}
	}
}

// recordHandler embeds NopHandler and records which requests it saw.
type recordHandler struct {
	NopHandler
	uploadPack  *UploadPackRequest
	receivePack *ReceivePackRequest
	lsRefs      *LsRefsRequest
	fetch       *FetchRequest
	objectInfo  *ObjectInfoRequest
	advertised  bool
}

func (h *recordHandler) AdvertiseRefs(ctx context.Context, s *Session) error {
	h.advertised = true
	return nil
}
func (h *recordHandler) UploadPack(ctx context.Context, s *Session, req *UploadPackRequest) error {
	h.uploadPack = req
	return nil
}
func (h *recordHandler) ReceivePack(ctx context.Context, s *Session, req *ReceivePackRequest) error {
	h.receivePack = req
	return nil
}
func (h *recordHandler) LsRefs(ctx context.Context, s *Session, req *LsRefsRequest) error {
	h.lsRefs = req
	return nil
}
func (h *recordHandler) Fetch(ctx context.Context, s *Session, req *FetchRequest) error {
	h.fetch = req
	return nil
}
func (h *recordHandler) ObjectInfo(ctx context.Context, s *Session, req *ObjectInfoRequest) error {
	h.objectInfo = req
	return nil
}

func pkt(items ...string) string {
	var b strings.Builder
	for _, s := range items {
		if s == "FLUSH" {
			b.WriteString("0000")
			continue
		}
		if s == "DELIM" {
			b.WriteString("0001")
			continue
		}
		n := len(s) + 4
		const hex = "0123456789abcdef"
		b.WriteByte(hex[(n>>12)&0xf])
		b.WriteByte(hex[(n>>8)&0xf])
		b.WriteByte(hex[(n>>4)&0xf])
		b.WriteByte(hex[n&0xf])
		b.WriteString(s)
	}
	return b.String()
}

func TestServerDispatchV0UploadPack(t *testing.T) {
	h := &recordHandler{}
	srv := &Server{Handler: h}
	oid := strings.Repeat("a", 40)
	in := pkt("want "+oid+" ofs-delta\n", "FLUSH", "done\n")
	s := &Session{Service: ServiceUploadPack, Version: 0, In: strings.NewReader(in), Out: &strings.Builder{}}
	if err := srv.Dispatch(context.Background(), s); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if h.uploadPack == nil {
		t.Fatal("handler did not receive UploadPackRequest")
	}
	if len(h.uploadPack.Wants) != 1 || !h.uploadPack.Done {
		t.Fatalf("decoded request = %+v", h.uploadPack)
	}
}

func TestServerDispatchV2Fetch(t *testing.T) {
	h := &recordHandler{}
	srv := &Server{Handler: h}
	oid := strings.Repeat("a", 40)
	in := pkt("command=fetch\n", "DELIM", "want "+oid+"\n", "done\n", "FLUSH")
	s := &Session{Service: ServiceUploadPack, Version: 2, In: strings.NewReader(in), Out: &strings.Builder{}}
	if err := srv.Dispatch(context.Background(), s); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if h.fetch == nil {
		t.Fatal("handler did not receive FetchRequest")
	}
}

func TestServerDefaultHandlerIsNop(t *testing.T) {
	srv := &Server{} // no handler
	oid := strings.Repeat("a", 40)
	in := pkt("want "+oid+"\n", "FLUSH", "done\n")
	s := &Session{Service: ServiceUploadPack, Version: 0, In: strings.NewReader(in), Out: &strings.Builder{}}
	err := srv.Dispatch(context.Background(), s)
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}

func TestWriteErrorFor(t *testing.T) {
	var b strings.Builder
	if err := WriteErrorFor(&b, ErrNotImplemented); err != nil {
		t.Fatalf("WriteErrorFor: %v", err)
	}
	if !strings.Contains(b.String(), "ERR not implemented") {
		t.Fatalf("output = %q", b.String())
	}

	b.Reset()
	if err := WriteErrorFor(&b, errors.New("secret detail")); err != nil {
		t.Fatalf("WriteErrorFor: %v", err)
	}
	if strings.Contains(b.String(), "secret") {
		t.Fatalf("generic error leaked detail: %q", b.String())
	}
	if !strings.Contains(b.String(), "internal server error") {
		t.Fatalf("output = %q", b.String())
	}
}
