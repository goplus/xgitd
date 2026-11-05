package protocol

import (
	"io"
	"strings"

	"github.com/goplus/xgitd/pktline"
)

// CapabilityAdvertisement describes the static v2 capability advertisement the
// server sends so a client knows which commands it may issue. The zero value
// advertises a sensible default set; use [DefaultCapabilityAdvertisement] to
// obtain a copy you can customize.
type CapabilityAdvertisement struct {
	// Agent is the server agent string, advertised as "agent=<Agent>". If
	// empty, DefaultAgent is used.
	Agent string
	// ObjectFormats lists the advertised object-format values (e.g. "sha1",
	// "sha256").
	ObjectFormats []string
	// LsRefs advertises the ls-refs command.
	LsRefs bool
	// Fetch advertises the fetch command. The FetchFeatures are appended as its
	// value, space-separated.
	Fetch bool
	// FetchFeatures are the sub-features advertised on the fetch line (for
	// example "shallow", "filter", "wait-for-done", "sideband-all").
	FetchFeatures []string
	// ObjectInfo advertises the object-info command.
	ObjectInfo bool
}

// DefaultAgent is the agent string advertised when none is configured.
const DefaultAgent = "git/xgitd"

// DefaultCapabilityAdvertisement returns the default v2 advertisement: ls-refs,
// fetch (with shallow/filter), and object-info, over SHA-1.
//
// The object-format capability advertises a single hash algorithm, since that
// is a property of the repository being served; it defaults to SHA-1 here
// because this skeleton has no backend to report the real one.
func DefaultCapabilityAdvertisement() CapabilityAdvertisement {
	return CapabilityAdvertisement{
		Agent:         DefaultAgent,
		ObjectFormats: []string{ObjectFormatSHA1},
		LsRefs:        true,
		Fetch:         true,
		FetchFeatures: []string{"shallow", "filter", "wait-for-done"},
		ObjectInfo:    true,
	}
}

// Lines returns the advertisement body as an ordered list of pkt-line payloads
// (without the trailing newline Git conventionally adds, which WriteV2 adds).
//
// Per the protocol-v2 grammar the advertisement begins with a "version 2" line,
// so that is the first element.
func (a CapabilityAdvertisement) Lines() []string {
	agent := a.Agent
	if agent == "" {
		agent = DefaultAgent
	}
	lines := []string{"version 2", "agent=" + agent}
	if len(a.ObjectFormats) > 0 {
		lines = append(lines, "object-format="+strings.Join(a.ObjectFormats, " "))
	}
	if a.LsRefs {
		lines = append(lines, "ls-refs")
	}
	if a.Fetch {
		line := "fetch"
		if len(a.FetchFeatures) > 0 {
			line += "=" + strings.Join(a.FetchFeatures, " ")
		}
		lines = append(lines, line)
	}
	if a.ObjectInfo {
		lines = append(lines, "object-info")
	}
	return lines
}

// WriteV2 writes the v2 capability advertisement to w as pkt-lines, each line
// terminated by a newline, followed by a flush-pkt. The caller is responsible
// for any transport-specific framing that precedes it (for example the Smart
// HTTP service banner).
func (a CapabilityAdvertisement) WriteV2(w io.Writer) error {
	pw := pktline.NewWriter(w)
	for _, line := range a.Lines() {
		if err := pw.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return pw.Flush()
}
