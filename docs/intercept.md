# Intercept (TLS-MITM)

Some dependencies cannot be repointed. A vendor SDK that always fetches its config from
`https://cdn.example.com/config.json` has no host setting to override — so pointing it at a mock is
not an option, and the call has to be intercepted.

```go
ic, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{})
defer ic.Stop(ctx)

ic.AddRules(ctx, riftembed.InterceptForward("cdn.example.com", imposterPort))

client, _ := ic.HTTPClient(ctx)   // trusts the CA, routes through the proxy
sut.SetHTTPClient(client)
```

That is the whole setup. Compare it with the usual alternative: a mitmproxy container, a Python
redirect addon, a CA certificate and private key committed to the repo, a JKS truststore, and JVM
system properties wiring it together.

## What the SDK can and cannot remove

!!! warning "The system under test must trust the intercept CA"
    This is inherent to TLS-MITM and no SDK can remove it. What *can* be removed is the
    provisioning work — generating a CA, minting per-host leaf certificates, and getting the
    result into the client's trust store.

`HTTPClient` returns a client that already trusts the CA and already proxies through the listener.
For a non-Go system under test, export a truststore instead:

```go
ic.ExportTruststore(ctx, "jks", "changeit", "/tmp/truststore.jks")
```

`format` is passed through to the engine, so a newer engine's format works without an SDK release.
`pem`, `pkcs12` and `jks` are the ones that exist today.

## Rules

Rules are evaluated in order; the first match wins.

```go
// Re-proxy a host's traffic, in plaintext, to a local imposter.
riftembed.InterceptForward("cdn.example.com", port)

// Answer inline, without an imposter.
riftembed.InterceptServe("flags.example.com", rift.OKJSON(flags))
```

For anything more selective, build the rule directly — `Predicates` are the standard predicates,
evaluated against the **decrypted** request:

```go
ic.AddRules(ctx, rift.InterceptRule{
	Predicates: []rift.Predicate{
		rift.PredicateOn("path", rift.StartsWith("/v2/")),
	},
	Action: rift.InterceptAction{
		Forward: &rift.InterceptTarget{Port: port},
	},
})
```

A rule with neither `Host` nor `Predicates` is a catch-all.

```go
rules, _ := ic.ListRules(ctx)
ic.ClearRules(ctx)     // leaves the listener running
```

## Forward vs serve

**Forward** re-proxies to a full imposter — so the request goes through the normal matching engine,
gets recorded, and can be asserted on with [`rifttest`](testing.md). This is usually what you want:

```go
port, _ := eng.CreateImposter(ctx, rift.NewImposter("cdn").Record().
	Stub(rift.OnGet("/config.json").Return(rift.OKJSON(cfg))))
ic.AddRules(ctx, riftembed.InterceptForward("cdn.example.com", port))

// … after exercising the SUT
recs, _ := eng.Recorded(ctx, port)   // the intercepted call is in the journal
```

**Serve** answers from the listener itself. Cheaper, but there is no imposter, so no journal and
nothing to verify against.

## The CA and TLS config

```go
pem, _ := ic.CACertPEM(ctx)          // raw PEM, for anything that needs it
cfg, _ := ic.TLSConfig(ctx)          // *tls.Config trusting the CA
url := ic.ProxyURL()                 // for http.ProxyURL / HTTPS_PROXY
```

`TLSConfig` seeds its pool from the **system roots** and adds the intercept CA, so a client using
it still reaches ordinary hosts — the interception is added, nothing is taken away.

## A complete example

```go
func TestFeatureFlagSDK(t *testing.T) {
	eng := rifttest.Engine(t).(*riftembed.Engine)
	ctx := t.Context()

	flags := rifttest.Imposter(t, rift.NewImposter("flags").
		Stub(rift.OnGet("/config.json").
			Return(rift.OKJSON(map[string]rift.JSON{"newCheckout": true}))))

	ic, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ic.Stop(ctx) })

	if err := ic.AddRules(ctx,
		riftembed.InterceptForward("cdn.example.com", flags.Port())); err != nil {
		t.Fatal(err)
	}

	client, err := ic.HTTPClient(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The SDK under test believes it is talking to the real CDN.
	sdk := vendorsdk.New(vendorsdk.WithHTTPClient(client))
	if !sdk.Enabled("newCheckout") {
		t.Error("flag should be on")
	}

	rifttest.AssertReceived(t, flags, rift.OnGet("/config.json"), rift.Once())
}
```

## Scope

Intercept covers the common case — intercept matched hosts, forward or serve. It is **not** a
general-purpose mitmproxy replacement: no flow scripting, no UI, and no attempt at exhaustive
protocol coverage. If you need those, use mitmproxy.
