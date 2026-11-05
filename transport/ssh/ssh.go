// Package ssh implements the SSH transport for a Git server: it accepts SSH
// session channels, parses the git-upload-pack / git-receive-pack exec request,
// reduces it to an [xgitd.Session], and drives the matching decoder.
//
// It is built on golang.org/x/crypto/ssh. Authentication is intentionally
// permissive: the public-key fingerprint is surfaced as [xgitd.Session.User]
// but is never used to authorize access. A real deployment supplies its own
// ssh.ServerConfig (via [Transport.Config]) if it wants to restrict who may
// connect.
package ssh

import (
	"context"
	"errors"
	"net"
	"strings"

	gossh "golang.org/x/crypto/ssh"

	"github.com/goplus/xgitd"
)

// Transport serves Git over SSH for a given [xgitd.Server].
type Transport struct {
	// Server dispatches decoded requests. Required.
	Server *xgitd.Server
	// Config is the SSH server configuration. It must have at least one host
	// key added. If NoClientAuth is false, the caller is responsible for
	// supplying auth callbacks; this transport only reads the fingerprint of a
	// public key, it does not decide acceptance.
	Config *gossh.ServerConfig
}

// fingerprintKey is the context key under which a successful public-key auth
// stashes the client's key fingerprint, so HandleConn can surface it.
type fingerprintKey struct{}

// Serve accepts connections from l until it returns an error (for example
// because l was closed) and handles each in its own goroutine.
func (t *Transport) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			_ = t.HandleConn(c)
		}()
	}
}

// ListenAndServe listens on the TCP address addr and serves Git over SSH. It is
// a convenience wrapper around net.Listen and [Transport.Serve].
func (t *Transport) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return t.Serve(l)
}

// HandleConn performs the SSH handshake on c and services its session
// channels. It closes c before returning.
func (t *Transport) HandleConn(c net.Conn) error {
	defer c.Close()
	if t.Config == nil {
		return errors.New("xgitd/ssh: Transport.Config is nil")
	}

	serverConn, chans, reqs, err := gossh.NewServerConn(c, t.Config)
	if err != nil {
		return err
	}
	defer serverConn.Close()
	go gossh.DiscardRequests(reqs)

	user := fingerprintFromPermissions(serverConn.Permissions)
	remoteAddr := ""
	if serverConn.RemoteAddr() != nil {
		remoteAddr = serverConn.RemoteAddr().String()
	}

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(gossh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		// Each session channel is serviced in its own goroutine so one idle or
		// slow client cannot block the others on the same connection.
		go t.handleSession(ch, chReqs, user, remoteAddr)
	}
	return nil
}

// handleSession processes the requests on one session channel. It handles a
// single exec (the Git command) preceded by any number of env requests, then
// closes the channel. A session that closes without an exec is closed here too,
// so no channel is leaked.
func (t *Transport) handleSession(ch gossh.Channel, reqs <-chan *gossh.Request, user, remoteAddr string) {
	defer ch.Close()
	var gitProtocol string
	for req := range reqs {
		switch req.Type {
		case "env":
			name, value := parseEnvRequest(req.Payload)
			if name == "GIT_PROTOCOL" {
				gitProtocol = value
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "exec":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			t.runExec(ch, execCommand(req.Payload), gitProtocol, user, remoteAddr)
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// runExec parses the Git command line, builds a Session, and drives the
// decoder, reporting the result to the client via the channel's exit status.
// The channel is closed by the caller (handleSession).
func (t *Transport) runExec(ch gossh.Channel, cmdline, gitProtocol, user, remoteAddr string) {
	service, repoArg, err := parseGitCommand(cmdline)
	if err != nil {
		_ = xgitd.WriteError(ch, err.Error())
		sendExit(ch, 1)
		return
	}

	repo, err := xgitd.NormalizeRepoPath(repoArg)
	if err != nil {
		_ = xgitd.WriteError(ch, "invalid repository path")
		sendExit(ch, 1)
		return
	}

	version := parseGitProtocol(gitProtocol)

	s := &xgitd.Session{
		Transport:  "ssh",
		Service:    service,
		Repo:       repo,
		Version:    version,
		User:       user,
		RemoteAddr: remoteAddr,
		In:         ch,
		Out:        ch,
	}

	ctx := context.Background()
	if err := t.serve(ctx, s); err != nil {
		_ = xgitd.WriteErrorFor(ch, err)
		sendExit(ch, 1)
		return
	}
	sendExit(ch, 0)
}

// serve runs the correct advertisement+dispatch sequence for the session.
//
// For v0 upload-pack and receive-pack, SSH begins with a ref advertisement
// before the client sends its request. For v2, the server advertises its
// capabilities first; the client then sends one command.
func (t *Transport) serve(ctx context.Context, s *xgitd.Session) error {
	if s.Version == 2 {
		if err := t.Server.WriteV2Advertisement(s); err != nil {
			return err
		}
		return t.Server.Dispatch(ctx, s)
	}
	if err := t.Server.AdvertiseRefs(ctx, s); err != nil {
		return err
	}
	return t.Server.Dispatch(ctx, s)
}

// parseGitProtocol reads the protocol version from a GIT_PROTOCOL value such as
// "version=2". Anything other than an explicit version=2 is treated as v0.
func parseGitProtocol(v string) int {
	for _, field := range strings.Split(v, ":") {
		if ver, ok := strings.CutPrefix(field, "version="); ok {
			if strings.TrimSpace(ver) == "2" {
				return 2
			}
		}
	}
	return 0
}

// fingerprintFromPermissions extracts a fingerprint stashed during public-key
// auth, if any.
func fingerprintFromPermissions(perms *gossh.Permissions) string {
	if perms == nil {
		return ""
	}
	return perms.Extensions["xgitd-fingerprint"]
}

// FingerprintAuthCallback returns a PublicKeyCallback that accepts every key and
// records its fingerprint so it surfaces as [xgitd.Session.User]. It is a
// convenience for the common "surface identity, authorize later" posture; real
// deployments can supply their own callback instead.
func FingerprintAuthCallback() func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
	return func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
		return &gossh.Permissions{
			Extensions: map[string]string{
				"xgitd-fingerprint": gossh.FingerprintSHA256(key),
			},
		}, nil
	}
}

// sendExit sends an exit-status request on the channel.
func sendExit(ch gossh.Channel, code uint32) {
	_, _ = ch.SendRequest("exit-status", false, exitStatusPayload(code))
}

// exitStatusPayload encodes an exit-status request payload (a big-endian
// uint32).
func exitStatusPayload(code uint32) []byte {
	return []byte{
		byte(code >> 24),
		byte(code >> 16),
		byte(code >> 8),
		byte(code),
	}
}
