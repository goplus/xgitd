# xgitd

`xgitd` is a lightweight Git server skeleton written in Go. It accepts
connections over **SSH** and **Smart HTTP**, speaks Git wire protocol **v0** and
**v2**, decodes every request into typed Go values, and hands each one to a
single [`Handler`](handler.go) interface.

## Package layout

```
xgitd/
├── pktline/            framing: read/write pkt-lines, flush/delim/response-end, side-band
├── protocol/           version/capability handling, v0 and v2 request decoders
├── transport/
│   ├── ssh/            SSH exec requests  -> Session
│   └── http/           Smart HTTP routes  -> Session
├── handler.go          Handler interface, Session, NopHandler, ErrNotImplemented
├── server.go           Server: decode-and-dispatch to the Handler
├── repopath.go         repository-path normalization
└── cmd/xgitd/          example binary wiring everything together
```

The layers are independent packages and usable on their own: `pktline` has no
dependency on `protocol`, and `protocol` has no dependency on the transports.

## What it decodes

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

## Usage

Embed `NopHandler` and override only the operations you care about:

```go
type logHandler struct{ xgitd.NopHandler }

func (logHandler) LsRefs(ctx context.Context, s *xgitd.Session, req *xgitd.LsRefsRequest) error {
	log.Printf("ls-refs repo=%s prefixes=%v symrefs=%v", s.Repo, req.RefPrefixes, req.Symrefs)
	return xgitd.ErrNotImplemented
}
```

See [`cmd/xgitd`](cmd/xgitd/main.go) for a runnable example that wires both
transports to a handler that just logs what it decodes:

```sh
go run ./cmd/xgitd -http :8080 -ssh :2222
```

Then point a real client at it (the commands fail because the handler is empty,
but the server logs the decoded request):

```sh
git -c protocol.version=2 ls-remote http://localhost:8080/org/repo.git
GIT_SSH_COMMAND='ssh -p 2222' git ls-remote git@localhost:org/repo.git
```

## Testing

```sh
go test ./...                       # unit, transport, and end-to-end tests
go test -short ./...                # skip the tests that shell out to real git
go test -fuzz FuzzReader ./pktline  # fuzz the pkt-line reader
```

The `e2e` package starts the server with a recording handler and drives the real
`git` client over both SSH and HTTP at protocol versions 0 and 2, asserting on
what the handler decoded. Those tests skip automatically when `git` (or, for the
SSH cases, `ssh`/`ssh-keygen`) is not on `PATH`.
