package protocol

import (
	"bufio"
	"io"
)

// peekReader reports whether r has any remaining data and, if so, returns a
// reader that yields the full remaining stream (the peeked byte included).
//
// It is used to decide whether a v0 push carried a packfile: a delete-only push
// sends no pack, so the stream is already at EOF after the command and
// push-option sections.
//
// When r is already a *bufio.Reader (as pktline.Reader.Remaining returns), it is
// peeked directly to avoid wrapping it in a second buffering layer for the whole
// packfile read.
func peekReader(r io.Reader) (io.Reader, bool, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	if _, err := br.Peek(1); err != nil {
		if err == io.EOF {
			return nil, false, nil
		}
		return nil, false, err
	}
	return br, true, nil
}
