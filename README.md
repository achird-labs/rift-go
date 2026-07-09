# rift-go

Official Go SDK for [Rift](https://github.com/EtaCassiopeia/rift) — a high-performance,
Mountebank-compatible HTTP/HTTPS mock server written in Rust.

> **Status: design phase.** API design and milestones are tracked in the issues of this repo
> (milestone M5). Nothing is importable yet.

## What it will look like

```go
func TestUserLookup(t *testing.T) {
    users := rifttest.Imposter(t,
        rift.Imposter("users").Record().
            Stub(rift.OnGet("/api/users/1").Return(rift.OKJSON(`{"id":1}`))))

    resp := callSUT(t, users.BaseURL())
    rifttest.AssertReceived(t, users, rift.OnGet("/api/users/1"), 1)
}
```

## Packages

| Package | Contents |
|---|---|
| `rift` | typed wire model + chainable builders, remote (admin API) + spawn transports |
| `riftembed` | in-process engine via [purego](https://github.com/ebitengine/purego) — no cgo, `CGO_ENABLED=0` builds keep working |
| `rifttest` | `testing.T` helpers: shared engine, `t.Cleanup` teardown, received-request assertions |
| `cmd/rift-fetch` | pre-fetch + SHA-verify the native library for CI / air-gapped hosts |

Full feature surface on every transport: stubs/predicates/responses, response cycling,
behaviors, proxy record/playback, fault injection, stateful scenarios, spaces/flow-state,
request verification, and TLS-MITM intercept with `tls.Config` helpers.
