package pktline

// Side-band channels, as used by the multiplexed sections of upload-pack and
// receive-pack responses.
const (
	// SideBandData is channel 1: packfile data.
	SideBandData byte = 1
	// SideBandProgress is channel 2: human-readable progress, shown on the
	// client's stderr.
	SideBandProgress byte = 2
	// SideBandError is channel 3: a fatal error message; the client aborts
	// after printing it.
	SideBandError byte = 3
)

// maxSideBandPayload is the largest amount of channel payload that fits in one
// pkt-line once the one-byte channel header is accounted for.
const maxSideBandPayload = MaxPayloadLen - 1

// SideBandWriter multiplexes payloads onto side-band channels, framing each
// chunk as a pkt-line whose first payload byte is the channel number.
//
// Writes to [SideBandData] larger than one pkt-line are split across as many
// packets as needed; progress and error writes are expected to be small and
// are written in a single packet (and rejected if too large).
type SideBandWriter struct {
	w *Writer
}

// NewSideBandWriter returns a [SideBandWriter] that frames onto w.
func NewSideBandWriter(w *Writer) *SideBandWriter {
	return &SideBandWriter{w: w}
}

// write emits one pkt-line on the given channel. The caller guarantees p fits.
func (sb *SideBandWriter) write(channel byte, p []byte) error {
	buf := make([]byte, 0, len(p)+1)
	buf = append(buf, channel)
	buf = append(buf, p...)
	return sb.w.WritePacket(buf)
}

// WriteData writes p on the data channel (1), splitting it across multiple
// pkt-lines as needed. It implements [io.Writer].
func (sb *SideBandWriter) WriteData(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxSideBandPayload {
			n = maxSideBandPayload
		}
		if err := sb.write(SideBandData, p[:n]); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

// Write implements [io.Writer] by writing to the data channel.
func (sb *SideBandWriter) Write(p []byte) (int, error) { return sb.WriteData(p) }

// WriteProgress writes a progress message on channel 2.
func (sb *SideBandWriter) WriteProgress(p []byte) error {
	if len(p) > maxSideBandPayload {
		return protoErr("progress message length %d exceeds maximum %d", len(p), maxSideBandPayload)
	}
	return sb.write(SideBandProgress, p)
}

// WriteError writes a fatal error message on channel 3.
func (sb *SideBandWriter) WriteError(p []byte) error {
	if len(p) > maxSideBandPayload {
		return protoErr("error message length %d exceeds maximum %d", len(p), maxSideBandPayload)
	}
	return sb.write(SideBandError, p)
}
