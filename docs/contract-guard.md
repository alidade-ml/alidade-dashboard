# The engine↔dashboard contract

astrolabe's `contract.py` is the source of truth for the `astrolabe.*` tag
names. The hub types them into Go source as string literals, and until
EXAMPLES-1.05 nothing checked they agreed.

The failure mode is silent. A tag renamed engine-side makes a tab return an
empty list, which is indistinguishable from "nobody logged any".

## Two surfaces, two guards, two different limits

| surface | guard | catches | does not catch |
|---|---|---|---|
| `astrolabe.*` tag names | `server/api/contract_test.go` | a Go literal disagreeing with the vendored `contract.py` | the engine moving on after the last re-vendor |
| Aim's object wire format | `server/api/aim_contract_test.go` (`-tags contract`) | Aim changing its encoding | anything, unless someone runs it |

Both limits are real and neither is hidden behind a green checkmark.

## The tag guard

`server/api/testdata/contract.py` is a **test fixture**, not shipped code —
nothing in the binary reads it. `contract-vendor.json` records the engine ref
it came from and its sha256, so a hand-edit to the fixture fails rather than
moving the goalpost under a wrong Go literal.

Re-vendor when the engine's contract changes:

```bash
tools/vendor-contract.sh [path-to-astrolabe-checkout]
(cd server && go test ./api/ -run TestContract)
```

`TestContractGuardCoversEveryAstrolabeLiteralInSource` scans the package for
`"astrolabe.*"` literals and fails on any that is not in the guard table. Without
it, adding a new tag to Go would leave it unguarded while the suite stayed green
— which is the failure this file exists to remove.

### Why vendored rather than read live

Parsing `contract.py` from a sibling astrolabe checkout would catch drift
immediately, and would need a checkout at a guessed path that a fresh clone does
not have — so `go test ./...` would pass by skipping. Vendoring is self-contained
and goes stale silently. That is the same trade the callbacks repo already made,
and consistency between the two readers is worth more than the difference.

## The encoding contract test

`aim_encoding_test.go` decodes captured bodies from `testdata/`. That pins the
decoder against its own regressions and **cannot tell us Aim changed**, because
the fixture only changes when we re-capture it.

Aim's encoding is an internal: `aim/storage/encoding/encoding_native.pyx`, no
version field, no negotiation, frame lengths in native byte order. If a future
Aim alters it, every unit test stays green while the hub mis-decodes — text
rendering as an empty string, which reads as a model that produced nothing.

Only a live server catches that:

```bash
mkdir -p /tmp/aim-contract && aim init --repo /tmp/aim-contract
aim up --host 127.0.0.1 --port 43896 --repo /tmp/aim-contract --yes &
cd server && go test -tags contract ./api/ -run TestAimContract \
  -aim-url=http://127.0.0.1:43896 -aim-repo=/tmp/aim-contract
```

It writes through the real Aim SDK, then reads back through the hub's decoder.
Shelling out to Python is deliberate: writing the format from Go would prove
only that our code is self-consistent with itself.

**Run it when bumping the `aim` dependency.** The hub has no CI, so nothing runs
it on a schedule — that is a stated limit, not an oversight. If the hub gains CI
this belongs on the nightly pattern astrolabe already uses for `pytest -m contract`.

### One thing it documents by needing it

The test indexes each run after writing it (`repo.request_props` +
`RepoIndexManager.index`). Without that the read returns nothing, because a run
written to a repo is invisible to an already-running server until it is indexed.
The sync sidecar does exactly this in production, and `repo.get_run` on an
unindexed repo returns `None` rather than raising — so nothing anywhere reports
the omission.
