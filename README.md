# xgitd

[![Build Status](https://github.com/goplus/xgitd/actions/workflows/go.yml/badge.svg)](https://github.com/goplus/xgitd/actions/workflows/go.yml)
[![Coverage Status](https://codecov.io/gh/goplus/xgitd/branch/main/graph/badge.svg)](https://codecov.io/gh/goplus/xgitd)

xgitd is a lightweight Git server skeleton written in Go. It accepts connections
over **SSH** and **Smart HTTP**, speaks Git wire protocol **v0** and **v2**.

## What it decodes and dispatches

| Protocol | Request | Handler method |
|---|---|---|
| v0 | ref advertisement | `AdvertiseRefs` |
| v0 | `git-upload-pack` (fetch/clone) | `UploadPack` |
| v0 | `git-receive-pack` (push) | `ReceivePack` |
| v2 | `ls-refs` | `LsRefs` |
| v2 | `fetch` | `Fetch` |
| v2 | `object-info` | `ObjectInfo` |

Version negotiation follows Git's rule: a client that asks for v2 (via
`GIT_PROTOCOL` over SSH or the `Git-Protocol` header over HTTP) gets v2;
everyone else — including a client whose header is stripped by a proxy — lands
on v0 without error. Unknown request arguments are not dropped silently; each
request type carries an `Unknown []string` field.

Over a stateful connection (SSH) a v2 client may issue several commands on one
channel — typically `ls-refs` then `fetch`. The server loops over them until the
client ends the sequence with a flush-pkt. Over stateless HTTP each command is
its own POST.

## Writing responses

The handler decides *what* to send; `xgitd/wire` frames it correctly on the wire.
Each response mechanism is available both as a method in the `internal/protocol`
package and, for convenience, as a `Session`-based helper on the `xgitd/wire`
package:

| Response | Helper |
|---|---|
| v0 ref advertisement | `WriteRefAdvertisementV0(s, RefAdvertisementV0{…})` |
| v0 upload-pack ACK/NAK + packfile | `NewUploadPackResponder(s, sideband)` |
| v0 receive-pack report-status | `WriteReportStatus(s, ReportStatus{…}, sideband)` |
| v2 ls-refs | `WriteLsRefs(s, req, refs)` |
| v2 fetch (acknowledgments / shallow-info / wanted-refs / packfile) | `NewFetchResponder(s)` |
| v2 object-info | `WriteObjectInfo(s, req, entries)` |

These handle the pkt-line framing, capability placement, empty-repository case,
side-band multiplexing of the packfile, and the v2 section delimiters. The
handler supplies the refs, acknowledgements, and packfile bytes; it never has to
hand-assemble pkt-lines. A handler is still free to write raw bytes to
`s.Out` when it needs to.

## Usage

Embed `wire.BaseHandler` and override only the operations you care about. A handler
that serves refs and a packfile looks like:

```go
import "github.com/goplus/xgitd/wire"

type repoHandler struct{ wire.BaseHandler }

func (h repoHandler) LsRefs(ctx context.Context, s *wire.Session, req *wire.LsRefsRequest) error {
	refs := []wire.Ref{{Name: "HEAD", ID: headOID, SymrefTarget: "refs/heads/main"}, /* … */}
	return wire.WriteLsRefs(s, req, refs)
}

func (h repoHandler) Fetch(ctx context.Context, s *wire.Session, req *wire.FetchRequest) error {
	resp := wire.NewFetchResponder(s)
	w, finish, err := resp.PackSection()
	if err != nil {
		return err
	}
	if _, err := w.Write(packBytes); err != nil { // packBytes produced by the backend
		return err
	}
	return finish()
}
```

See [`cmd/xgitd`](cmd/xgitd/main.go) for a runnable logging example, and
[`e2e/clone_test.go`](e2e/clone_test.go) for a worked handler backed by a real
repository that `git clone` and `git fetch` succeed against:

```sh
go run ./cmd/xgitd -http :8080 -ssh :2222
```

## Authorization

By default every session is allowed and authorization is left to the handler.
To gate access across both transports from one place, set `Server.Authorizer`.
The hook runs after the transport has populated the `Session` (`Service`,
`Repo`, `User`, `RemoteAddr`, `Version`) but before any advertisement or request
dispatch — so a rejected session never reaches the `Handler`:

```go
srv := &wire.Server{
	Handler: repoHandler{},
	Authorizer: wire.AuthorizeFunc(func(ctx context.Context, s *wire.Session) error {
		if s.Service == wire.ServiceReceivePack && !mayPush(s.User, s.Repo) {
			return fmt.Errorf("%q may not push to %q: %w", s.User, s.Repo, wire.ErrUnauthorized)
		}
		return nil
	}),
}
```

Return `nil` to allow, `wire.ErrUnauthorized` (optionally wrapped with context)
to reject, or any other error to abort as an internal failure. A rejection
becomes an `ERR unauthorized` pkt-line over SSH and an HTTP `403 Forbidden` over
Smart HTTP; the wrapped detail stays server-side and never leaks to the client.
Decide on the metadata alone: `s.In` may be nil (and must not be read) and
`s.Out` must not be written. Note the hook runs per request, not per operation,
so over Smart HTTP a single clone/fetch/push invokes it twice (on `info/refs`
and on the POST); keep it idempotent and side-effect-free. The example binary's
`-read-only` flag shows the pattern.

## Testing

```sh
go test ./...                                # unit, transport, and end-to-end tests
go test -short ./...                         # skip the tests that shell out to real git
go test -fuzz FuzzReader ./internal/pktline  # fuzz the pkt-line reader
```

The `e2e` package drives the real `git` client over both SSH and HTTP at
protocol versions 0 and 2: `ls-remote` against a recording handler, and a full
`clone`/`fetch` against a handler backed by a real repository (proving the
response writers produce a clonable stream). Those tests skip automatically when
`git` (or, for the SSH cases, `ssh`/`ssh-keygen`) is not on `PATH`.
