# Explain validation: existing Prometheus history

Used `prometheus-history.json` from the historical validation, without checking
out revisions, invoking Go for target builds, or modifying the artifact.

```sh
gorebuild explain --history prometheus-history.json --commit 503180645ef0
gorebuild explain --history prometheus-history.json --package ./web/api/v1
```

The event explanation reproduces:

- Rebuild wall: **19.263s**; warmed no-op baseline: **0.565s**.
- Executed nodes: **63 build + 1 link**, with no incremental cgo/assembly nodes.
- Compilation tool work: **35.909s**; linking tool work: **4.578s**.
- Estimated executed critical path: **18.845s**, following histogram,
  TSDB/chunk/storage dependencies, `web/api/v1`, `web`, `cmd/prometheus`, linking.
- API action: **3.647s elapsed**, **3.469s recorded tool work**.

An independent DAG traversal verifies the zero-duration API counterfactual:
current path **18.844518927s**, remaining longest path **15.197720481s**, maximum
modeled reduction **3.646798446s**. Competing dependencies remain in the graph.
This is a theoretical bound on the fixed weighted DAG, not predicted wall-clock
savings. A synthetic competing-branch test separately verifies that removing a
75s action can improve the path by only 5s, and an off-path action can save zero.

The package explanation finds API Go compilation in **15/18** usable successful
samples (**20 selected, 2 excluded**), with typical recorded package-node work
**2.819s** and elapsed contribution **2.965s**. Only one of those events directly
changed API itself. Co-occurring packages and dependency routes are displayed as
inferred relationships, not invalidation causality. No whole-event latency is
presented as API's own compile time.

An opt-in current-source inspection also ran:

```sh
gorebuild explain --history prometheus-history.json --package ./web/api/v1 \
  --inspect-consumer ./web --source .
```

The updated `go/packages` loader resolves **33 referenced exports**, including
`API`, `API.Register`, `GlobalURLOptions`, and **697 reachable provider
declarations**, including unexported helpers. **0 opaque stubs, 0 type diagnostics;
complete type checking.** This replaces the previous 84 opaque imports and
thousands of partial-check diagnostics. The consumer recompiled in **15/18**
successful samples; the provider-to-consumer historical DAG route appears in
those 15 events. Current-source object bindings are not proof of historical
cache invalidation or removability. Interface satisfaction, reflection, dynamic
calls and initialization remain relevant constraints.

Validation used the captured linux/amd64 settings, `GOWORK=off`, read-only module
selection and the same Go version (`go1.27.3-X:nodwarf5`). **No target or dependency
compilation was requested**: the loader used `-n`, cached export data/source
fallback. No generators or initialization code ran. Full selected source evidence
is about 270 KiB, not another copy of the 337 MiB history capture. The command's
new `--out` stores all declarations/edges and diagnostics without terminal dumps.
The event wall, tool work, critical path and counterfactual values above were
rechecked and remain unchanged.

Cache-hash replay is a separate, explicit `--replay-cache PACKAGE --commit HASH
--source CHECKOUT --out FILE` operation; at this earlier validation stage it was **not run on Prometheus**. The subsequent real replay and suggestion validation are documented in `suggest-validation.md`. A real
synthetic-repository test verifies parent/warm/child collection, selected hash
blocks, no-op verification, developer checkout preservation, untouched normal
build cache and worktree cleanup. Parser tests distinguish changed file hashes,
imported content hashes, non-Go inputs and VCS metadata, including repeated hash
writes. Source references alone never become an invalidation-causality claim.

Tests also cover gzip/plain input, malformed/corrupt graphs, ambiguous prefixes,
legacy graph fallback, saved paired VCS observations, cache checks versus real
execution, overlapping package cohorts and competing paths. Typed-source tests
cover real imported types, isolated uncached source fallback without compiled
archives, explicit export-build permission, unrelated broken packages excluded
from loading, tags/platform selection, generics, embedded fields, interface
methods, generated source and incomplete type checking. Default offline
explanation tests remove Go/compiler/Git executables from `PATH` and verify input
bytes are unchanged.
