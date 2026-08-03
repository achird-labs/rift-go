# Conformance

Every official Rift SDK replays the engine's **SDK conformance corpus**. It is the single source of
truth for DSL ↔ engine parity: a fixture the typed DSL cannot express is a red build.

rift-go passes all fixtures on **both** transports.

## The two gates

### 1. DSL-expressibility

Each fixture is parsed, reconstructed through the typed model, and re-serialized. The output must
**deep-equal** the fixture.

This runs without an engine, so a DSL regression is caught even on a machine with no native
library. It is also the gate that matters most: if the model silently drops a field, every test
written against it is asserting something other than what reaches the engine.

Permitted normalizations are exactly those `rift-lint` treats as equivalent:

- an omitted key and an explicitly-default one (`"recordRequests": false`)
- an explicit `null` on an optional key — both deserialize to nothing

Everything else is a failure. That is what the `Extra` round-trip in the [wire model](dsl.md#the-escape-hatch)
exists for: unknown keys survive untouched, so a fixture using grammar the builders do not model
still passes.

### 2. Serve and replay

Each fixture's imposter is created and its `_verify` transcript driven request by request, with
each `expect` asserted.

## Running it

```sh
RIFT_CORPUS_DIR=/path/to/sdk-conformance go test ./conformance/...
```

The corpus is a release asset, version-locked to the engine:

```sh
gh release download v0.17.0 --repo achird-labs/rift --pattern 'sdk-conformance-v0.17.0.tar.gz'
tar -xzf sdk-conformance-v0.17.0.tar.gz
```

Resolution order: an explicit directory, `$RIFT_CORPUS_DIR`, then a sibling checkout of the engine
repo — the last so a contributor with both repos cloned needs no setup.

## Capability gates

A fixture may be skipped **only** when its `requires` names a capability the lane lacks —
`injection`, `proxy`, `redis`, `https`, `shell`. Never for any other reason.

Because a run that silently skipped everything would still report green, CI asserts that both lanes
actually ran:

```yaml
- run: |
    go test -count=1 -v ./conformance/... 2>&1 | tee conformance.log
    for lane in embedded remote; do
      grep -q -- "--- PASS: TestServeAndReplay/$lane" conformance.log \
        || { echo "::error::the $lane lane did not run"; exit 1; }
    done
```

## The corpus is a coherent set

Fixtures are **not** independent. `07-proxy-record` proxies to `http://localhost:4501`, which is
`01-basic-rest`'s manifest port — so every fixture is created up front, on its declared port,
before any transcript runs. Creating them one at a time on engine-assigned ports leaves the proxy
fixture pointing at an upstream that no longer exists.

The manifest `port` is a fixture's identity, and the replay asserts the engine honours it verbatim.

## What it has caught

The gate is not ceremony. Building this SDK, it found three model bugs before any user could:

- **`exists` takes the same nested field shape as its siblings** —
  `{"exists":{"headers":{"Authorization":true}}}`, not the flat `field→bool` map it superficially
  resembles.
- **`verify`'s `closest` is an object**, carrying which clauses the nearest request failed and its
  actual values — not an array. Modelling it properly is what turns a failed assertion from
  "expected 1, got 0" into a diff.
- **Explicit `null` on an optional key** round-trips as absent rather than being lost.

## Adding coverage

Fixtures live in the engine repo and are numbered and append-only, so a fixture's identity is
stable across versions. Add one there — the engine's own gate enforces that it serves and its
`_verify` transcripts hold before it can ship — and every SDK picks it up on the next version bump.
