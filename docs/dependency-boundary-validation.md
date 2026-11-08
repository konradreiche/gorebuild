# Incoming dependency-boundary validation

## Commands and safety

Ran in `/home/konrad/src/prometheus`, with compilation-disabled package loading:

```sh
GOWORK=off gorebuild suggest ./web/api/v1 \
  --history prometheus-history.json --out prometheus-suggestions.json
GOWORK=off gorebuild suggest ./web/api/v1 \
  --history prometheus-history.json --cache-evidence prometheus-cache.json \
  --out prometheus-suggestions-cache.json
```

Both completed. SHA-256 checksums of the history and cache artifacts were unchanged.
Git status changed only by the two requested output files; worktree registrations
matched before/after. No source changes, generators, export compilation, target
builds or new cache replays were requested. Source loading used Go's `-n` mode,
read-only modules, linux/amd64, captured flags and `GOWORK=off`.

## A credible incoming-boundary opportunity

The target has **63 resolved direct imports**. Storage, TSDB, PromQL and histogram
all have declaration/symbol references and event evidence in structured output.

`storage/remote` has a particularly bounded construction boundary:

- **Resolved:** Its only requiring target declaration is `NewAPI`. All dependency
  references are inside three factory calls: `NewReadHandler`, `NewWriteHandler`
  and `NewOTLPWriteHandler` (including the latter's `OTLPOptions` literal).
- **Resolved:** Every factory result is the dependency-neutral `net/http.Handler`.
  API already stores these results in handler fields. No remote-specific type
  leaks through the target's constructor signature or retained types/state.
- **Resolved:** `web` contains a typed call to `NewAPI`, providing an existing
  composition site. Factory construction arguments come from constructor
  parameters, not inaccessible target-local variables.
- **Modeled:** Inject the three handler values into API and construct them in
  `web`, carrying options/feature gates with construction. Remove
  `web/api/v1 -> storage/remote`; add `web -> storage/remote`, preserving
  `web -> web/api/v1`. All target references must be migrated: keeping a default
  or compatibility factory wrapper in API would defeat the boundary.
- **Structural check:** Other retained API imports do not transitively reach
  `storage/remote`; the proposed graph has no new import cycle. Current-config
  structural support is recorded explicitly. This is not a method/type-family
  extraction, nor a relocation of the 697/722-declaration API closure.
- **Git evidence:** 24 recorded events directly changed remote source. `NewAPI`'s
  syntactic fingerprint was unchanged in 23, changed in one, unknown in zero.
  This is source-history evidence, not proof of cache behavior.
- **Measured:** Ten sampled relevant events recompiled API, nine without API
  source changes. Its compilation lay on the selected critical path in those
  nine. These include changes to upstream inputs used by remote, not just edits
  to remote itself.

This is a **structurally supported opportunity requiring investigation**, not a
verified safe refactoring. Construction/registration order, initialization,
feature gates, errors, tests and callers outside the captured target closure
must be reviewed. Other platforms/build tags remain unverified.

**No isolated measured benefit is claimed.** The sampled events also changed
other retained API inputs; none supports attributing an avoidable API action to
this boundary alone. No package-duration-as-savings or zero-future-benefit claim
is made. Actual cache reuse and new composition costs require implementation and
before/after verification in future work.

## Cache evidence and direct source changes

The selected cache artifact is a separate diagnostic replay for `503180645ef0`.
Its raw hash blocks are reanalyzed and checked against package, commit, flags,
toolchain and captured environment; main/link blocks cannot become library-input
causes. The target compiler hash changed **12 imported content inputs** (including
remote) **and `json_codec.go`**. All relevant incoming evidence records the direct
API source edit. Every boundary impact for this event is excluded with the reason
that removing an import cannot prevent a compilation with target source changes.
No sufficient individual invalidation cause is inferred from those concurrent
changes or from reverse-dependency paths.

Missing/incomplete/ambiguous/mismatched cache evidence cannot support observed-input
claims or conditional avoided-action estimates. Unsupported neutral-result
inversion, captured locals, foreign type leakage, retained transitive routes and
cycles are explicitly rejected or left for investigation.

## Preserved outgoing analysis and complexity reporting

All **12 outgoing extraction groups remain rejected**. Small utilities/interfaces
still leave `web -> web/api/v1`; broad type/method closures are not promoted to
architectural recommendations.

Category C retains measured package compilation work, inventories large AST
nodes, generated files, generic instantiations and composites, and explicitly
requires compiler profiling before any specific optimization. Source size is not
used as measured compiler attribution or a savings score.

Synthetic incoming tests demonstrate a neutral-interface factory boundary, stable
and changed declaration history, a closed extraction that really removes exposure,
wrapper/alternate-route/cycle rejection, incomplete types/cache evidence, and
direct source changes withholding benefits. An eligible synthetic event zeroes a
40s target action but reduces the 80s critical path only to 75s due to competition:
**5s fixed-DAG theoretical upper bound**, excluding new adapter costs, not a
prediction. Existing read-only and no-compiler-archive tests remain in place.
