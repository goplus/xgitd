package pktline

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadPacketSpecial(t *testing.T) {
	tests := []struct {
		name string
		in   string
		kind Kind
	}{
		{"flush", "0000", KindFlush},
		{"delim", "0001", KindDelim},
		{"response-end", "0002", KindResponseEnd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(strings.NewReader(tt.in))
			p, err := r.ReadPacket()
			if err != nil {
				t.Fatalf("ReadPacket: %v", err)
			}
			if p.Kind != tt.kind {
				t.Fatalf("kind = %v, want %v", p.Kind, tt.kind)
			}
			if p.Data != nil {
				t.Fatalf("special packet carried data %q", p.Data)
			}
		})
	}
}

func TestReadPacketData(t *testing.T) {
	// "0006a\n" => length 6, payload "a\n".
	r := NewReader(strings.NewReader("0006a\n"))
	p, err := r.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if p.Kind != KindData {
		t.Fatalf("kind = %v, want data", p.Kind)
	}
	if got := string(p.Data); got != "a\n" {
		t.Fatalf("payload = %q, want %q", got, "a\n")
	}
}

func TestReadPacketReserved0003(t *testing.T) {
	r := NewReader(strings.NewReader("0003"))
	_, err := r.ReadPacket()
	var pe *ErrProtocol
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *ErrProtocol", err)
	}
}

func TestReadPacketBadLength(t *testing.T) {
	r := NewReader(strings.NewReader("00zz"))
	_, err := r.ReadPacket()
	var pe *ErrProtocol
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *ErrProtocol", err)
	}
}

func TestReadPacketBoundaryLengths(t *testing.T) {
	// Smallest data packet: length 5 ("0005" + one byte).
	t.Run("min", func(t *testing.T) {
		r := NewReader(strings.NewReader("0005x"))
		p, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}
		if string(p.Data) != "x" {
			t.Fatalf("payload = %q, want %q", p.Data, "x")
		}
	})

	// Largest valid packet: total 65520 (payload 65516).
	t.Run("max", func(t *testing.T) {
		payload := bytes.Repeat([]byte("z"), MaxPayloadLen)
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if err := w.WritePacket(payload); err != nil {
			t.Fatalf("WritePacket: %v", err)
		}
		if buf.Len() != MaxPacketLen {
			t.Fatalf("encoded length = %d, want %d", buf.Len(), MaxPacketLen)
		}
		r := NewReader(&buf)
		p, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}
		if len(p.Data) != MaxPayloadLen {
			t.Fatalf("payload len = %d, want %d", len(p.Data), MaxPayloadLen)
		}
	})

	// Too large: 65521. We hand-craft the header "fff1".
	t.Run("over-max", func(t *testing.T) {
		r := NewReader(strings.NewReader("fff1"))
		_, err := r.ReadPacket()
		var pe *ErrProtocol
		if !errors.As(err, &pe) {
			t.Fatalf("err = %v, want *ErrProtocol", err)
		}
	})
}

func TestReadPacketTruncated(t *testing.T) {
	t.Run("partial-header", func(t *testing.T) {
		r := NewReader(strings.NewReader("00"))
		_, err := r.ReadPacket()
		if err != io.ErrUnexpectedEOF {
			t.Fatalf("err = %v, want ErrUnexpectedEOF", err)
		}
	})
	t.Run("truncated-payload", func(t *testing.T) {
		// Declares 6 bytes total (2 payload), supplies 1.
		r := NewReader(strings.NewReader("0006a"))
		_, err := r.ReadPacket()
		if err != io.ErrUnexpectedEOF {
			t.Fatalf("err = %v, want ErrUnexpectedEOF", err)
		}
	})
}

func TestReadPacketCleanEOF(t *testing.T) {
	r := NewReader(strings.NewReader(""))
	_, err := r.ReadPacket()
	if err != io.EOF {
		t.Fatalf("err = %v, want EOF", err)
	}
}

func TestWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteString("want 1234\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := w.Delim(); err != nil {
		t.Fatalf("Delim: %v", err)
	}
	if err := w.WriteString("done\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	r := NewReader(&buf)
	want := []struct {
		kind Kind
		data string
	}{
		{KindData, "want 1234\n"},
		{KindDelim, ""},
		{KindData, "done\n"},
		{KindFlush, ""},
	}
	for i, exp := range want {
		p, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if p.Kind != exp.kind {
			t.Fatalf("packet %d: kind = %v, want %v", i, p.Kind, exp.kind)
		}
		if string(p.Data) != exp.data {
			t.Fatalf("packet %d: data = %q, want %q", i, p.Data, exp.data)
		}
	}
}

func TestWriteEmptyPacketRejected(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WritePacket(nil); err == nil {
		t.Fatal("WritePacket(nil) = nil, want error")
	}
	if err := w.WritePacket([]byte{}); err == nil {
		t.Fatal("WritePacket(empty) = nil, want error")
	}
}

func TestWriteOversizePayloadRejected(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WritePacket(bytes.Repeat([]byte("x"), MaxPayloadLen+1)); err == nil {
		t.Fatal("WritePacket(oversize) = nil, want error")
	}
}

func TestEncodeLen(t *testing.T) {
	tests := []struct {
		length int
		want   string
	}{
		{0, "0000"},
		{1, "0001"},
		{4, "0004"},
		{6, "0006"},
		{MaxPacketLen, "fff0"},
	}
	for _, tt := range tests {
		var hdr [HeaderLen]byte
		encodeLen(&hdr, tt.length)
		if got := string(hdr[:]); got != tt.want {
			t.Errorf("encodeLen(%d) = %q, want %q", tt.length, got, tt.want)
		}
	}
}

func TestSideBandWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	sb := NewSideBandWriter(w)

	if _, err := sb.WriteData([]byte("PACK...")); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	if err := sb.WriteProgress([]byte("counting\n")); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}
	if err := sb.WriteError([]byte("boom\n")); err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	r := NewReader(&buf)
	expect := []struct {
		channel byte
		payload string
	}{
		{SideBandData, "PACK..."},
		{SideBandProgress, "counting\n"},
		{SideBandError, "boom\n"},
	}
	for i, exp := range expect {
		p, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if p.Kind != KindData {
			t.Fatalf("packet %d: kind = %v", i, p.Kind)
		}
		if p.Data[0] != exp.channel {
			t.Fatalf("packet %d: channel = %d, want %d", i, p.Data[0], exp.channel)
		}
		if got := string(p.Data[1:]); got != exp.payload {
			t.Fatalf("packet %d: payload = %q, want %q", i, got, exp.payload)
		}
	}
}

func TestSideBandDataSplit(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	sb := NewSideBandWriter(w)

	// Two full side-band chunks plus a remainder.
	payload := bytes.Repeat([]byte("q"), maxSideBandPayload*2+10)
	n, err := sb.WriteData(payload)
	if err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("WriteData returned %d, want %d", n, len(payload))
	}

	r := NewReader(&buf)
	var got []byte
	for {
		p, err := r.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ReadPacket: %v", err)
		}
		if p.Data[0] != SideBandData {
			t.Fatalf("unexpected channel %d", p.Data[0])
		}
		got = append(got, p.Data[1:]...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("reassembled payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func FuzzReader(f *testing.F) {
	seeds := []string{
		"",
		"0000",
		"0001",
		"0002",
		"0003",
		"0005x",
		"0006a\n",
		"00zz",
		"fff1",
		"00040000",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data))
		for i := 0; i < 1000; i++ {
			p, err := r.ReadPacket()
			if err != nil {
				// Any error terminates the loop; the contract is only that we
				// never panic and never report a data packet larger than the
				// maximum.
				return
			}
			if p.Kind == KindData && len(p.Data) > MaxPayloadLen {
				t.Fatalf("data packet larger than max: %d", len(p.Data))
			}
		}
	})
}
