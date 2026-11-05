package protocol

import (
	"strings"
)

// Capabilities is a parsed set of protocol capabilities. Capabilities appear in
// two shapes on the wire: bare flags ("ofs-delta", "thin-pack") and key=value
// pairs ("agent=git/2.40.0", "object-format=sha256"). Both are stored here; a
// bare flag is recorded with an empty value.
//
// The zero value is not usable; build one with [ParseCapabilities].
type Capabilities struct {
	// values maps capability name to its value (empty for bare flags).
	values map[string]string
	// order preserves the order capabilities were seen, for stable iteration.
	order []string
}

// ParseCapabilities parses a space-separated capability list, the form that
// rides along after the NUL on a v0 first want/command line. Empty tokens are
// ignored.
func ParseCapabilities(s string) *Capabilities {
	c := &Capabilities{values: make(map[string]string)}
	for tok := range strings.FieldsSeq(s) {
		c.add(tok)
	}
	return c
}

// newCapabilities returns an empty capability set, ready for Add.
func newCapabilities() *Capabilities {
	return &Capabilities{values: make(map[string]string)}
}

// add records a single "name" or "name=value" token.
func (c *Capabilities) add(tok string) {
	if tok == "" {
		return
	}
	name, value, _ := strings.Cut(tok, "=")
	if _, seen := c.values[name]; !seen {
		c.order = append(c.order, name)
	}
	c.values[name] = value
}

// Has reports whether the named capability is present (as a flag or a pair).
func (c *Capabilities) Has(name string) bool {
	if c == nil {
		return false
	}
	_, ok := c.values[name]
	return ok
}

// Get returns the value of a key=value capability. The boolean reports whether
// the capability was present at all; a present bare flag returns ("", true).
func (c *Capabilities) Get(name string) (string, bool) {
	if c == nil {
		return "", false
	}
	v, ok := c.values[name]
	return v, ok
}

// Value returns the value of a capability, or "" if absent.
func (c *Capabilities) Value(name string) string {
	v, _ := c.Get(name)
	return v
}

// Names returns the capability names in the order they were first seen.
func (c *Capabilities) Names() []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// Len reports how many distinct capabilities are present.
func (c *Capabilities) Len() int {
	if c == nil {
		return 0
	}
	return len(c.values)
}

// Agent returns the client agent string, if the client sent one.
func (c *Capabilities) Agent() string { return c.Value("agent") }

// ObjectFormat returns the negotiated object-format name, or [ObjectFormatSHA1]
// if the client did not specify one.
func (c *Capabilities) ObjectFormat() string {
	if v := c.Value("object-format"); v != "" {
		return v
	}
	return ObjectFormatSHA1
}
