# Transports

Three ways to reach an engine. All implement `rift.Client`, so a suite written against one runs
against the others without changing test code — which is also what lets the
[conformance corpus](conformance.md) assert that they behave identically.

| | `riftembed.Start` | `rift.Connect` | `rift.Spawn` |
|---|---|---|---|
| Engine location | your process | already running | a child process |
| Needs the native library | **yes** | no | no |
| Needs the `rift` binary | no | no | **yes** |
| Startup cost | milliseconds | none | process start |
| Best for | unit and integration tests | shared or containerised engines | integration tests without the cdylib |

## Embedded

See [Embedded engine](embedded.md).

```go
eng, err := riftembed.Start(riftembed.Options{})
defer eng.Close()
```

## Connect — an engine already running

```go
eng, err := rift.Connect("http://localhost:2525", rift.RemoteOptions{
	APIKey: os.Getenv("RIFT_API_KEY"),   // only if the engine was started with one
})
defer eng.Close()

if err := eng.Ping(ctx); err != nil {
	// ErrEngineUnavailable — nothing answered
}
```

`Connect` does not contact the engine; it just builds a client. Call `Ping` when you want to know
whether anything is there.

### Reaching imposters on another host

`RemoteOptions.Host` overrides where imposters are addressed, which matters when the admin API and
the imposter ports are not on the same host as your test:

```go
eng, _ := rift.Connect("http://rift.internal:2525", rift.RemoteOptions{
	Host: "rift.internal",
})
url := rift.BaseURLFor(eng, "http", port)   // http://rift.internal:4545
```

`BaseURLFor` asks the transport where an imposter lives, so it works unchanged across all three.

### Custom HTTP client

```go
rift.Connect(adminURL, rift.RemoteOptions{
	HTTPClient: &http.Client{Timeout: 5 * time.Second},
})
```

## Spawn — a managed child process

```go
eng, err := rift.Spawn(ctx, rift.SpawnOptions{
	Args: []string{"--allowInjection", "--loglevel", "error"},
})
defer eng.Close()   // stops the process
```

`Spawn` picks a free admin port unless you pin one, so parallel test binaries do not collide, and
it does not return until the admin API answers — a client that raced startup would fail
intermittently in exactly the way that is hardest to debug.

It also notices a child that **died** during startup, rather than waiting out the timeout. Without
that, a crash-on-startup reads as "slow machine" and sends people looking in the wrong place.

!!! warning "ctx bounds startup, not the process"
    `Spawn` deliberately uses `exec.Command`, not `exec.CommandContext`.

    `CommandContext` ties the child's lifetime to the context, so the natural
    `ctx, cancel := context.WithTimeout(...); defer cancel()` would kill the engine the moment
    the calling function returned. The symptom is a refused connection on the first request —
    nothing that points at lifetime.

    **The engine runs until `Close`.** If you want it to stop early, call `Close`.

### Finding the binary

`Binary`, then `$RIFT_BINARY`, then `rift` on `PATH`, then `rift-http-proxy` on `PATH` — the last
because a cargo build of the engine produces that name, so a contributor running against a local
build needs no extra setup.

### Working directory

`SpawnOptions.Dir` sets the child's working directory. This matters for configs with relative data
paths — the conformance corpus, for instance, resolves `data/…` relative to the corpus root.

## Choosing

- **Writing unit tests?** Embedded. It is the fastest and needs no ports.
- **No native library for your platform?** `Spawn`, or `Connect` to a container.
- **Engine shared across a test suite or a compose stack?** `Connect`.
- **Running in CI on an unusual architecture?** `Connect` or `Spawn` — neither needs the cdylib.
