# Cache invalidation and suggestion validation: Prometheus

This records the original outgoing-only suggestion analysis. The subsequent
incoming-boundary investigation and supported factory-inversion opportunity are
in `dependency-boundary-validation.md`; outgoing rejections below remain valid.

## Real cache replay

Ran the requested `GOWORK=off gorebuild explain --history prometheus-history.json
--replay-cache ./web/api/v1 --commit 503180645ef0 --source .
--out prometheus-cache.json` in the Prometheus checkout. The replay completed with
verified warm-parent no-op and complete selected API hash evidence. Temporary
worktree registrations were removed. Before/after worktree listings and normal
Go cache file sizes/mtimes matched exactly. Git status differed only by the new
requested `prometheus-cache.json`; tracked source/index/HEAD were unchanged.

Observed warmed-parent to child inputs:

| Action | Changed inputs |
| --- | --- |
| API compilation | `json_codec.go` content hash; 12 imported package content hashes |
| Main compilation | `main.go` content hash; 15 imported package content hashes; module/VCS build metadata |
| Main linking | 61 changed `packagefile` content inputs, plus full main build ID |

API's changed imports were config, model/histogram, promql, promql/parser, rules,
scrape, storage, storage/remote, tsdb, tsdb/index, util/httputil and util/jsonutil.
The stamped main metadata changed the module pseudo-version, `vcs.revision`
(`e4334de10cb9...` to `503180645ef0...`) and `vcs.time`. No selected compiler tool,
platform, build-flag or environment hash change was established. The main build
ID is a link input, not a toolchain configuration change.

These inputs agree with executed API/main compilation and main linking in the
historical action graph. Source and imported content both changed, so neither
can be isolated as the sole sufficient cause. VCS metadata contributed to the
main cache key; source/import changes still exist independently, so this is not
proof that disabling stamping would avoid this event's compilation/linking.
Declaration-level causality, generator provenance, and individual sufficient
invalidation causes remain unresolved. Unselected package hash blocks were not
collected and are not assigned invented causes.

The replay exposed repeated identical hash invocations in warmed builds (the
main link block appeared twice). The parser now compares hash invocations rather
than concatenating them, preventing false claims that unchanged configuration
inputs disappeared. Conflicting hashes/truncated blocks are explicitly incomplete.
The saved artifact's derived differences were reanalyzed after this fix; raw logs
were preserved unchanged. An opt-in test using `GOREBUILD_PROMETHEUS_CACHE` verifies
the production parser against that real artifact without another expensive build.

## Suggestions

Ran `GOWORK=off gorebuild suggest ./web/api/v1 --history prometheus-history.json`.
The package and consumer `web` each recompile in **15/18** successful samples.
Current-source type checking resolves without errors. No small, structurally
supported extraction eliminates `web -> web/api/v1`:

- `FuzzAlgorithms`: three-declaration closure (including unexported state);
  moving it leaves `web`'s API/registration/constructor references.
- `GlobalURLOptions`: four declarations including fields; moving it likewise
  retains the API dependency.
- Small retriever interfaces are coherent type/method families, but extracting
  them alone leaves other direct references to the provider.
- Required API type/method families expand to roughly 716 declarations; the
  constructor closure reaches 722. These are rejected as broad package-sized
  moves, not useful small extractions. The earlier 697 reachable declarations
  were not interpreted as an architectural recommendation.

All 12 distinct groups are rejected. Rejected groups receive no invented stability
or savings estimate: even stable utility declarations cannot isolate `web` while
it still imports API. The recurring API -> web -> main path therefore remains.
Inversion/interface redesign or relocating whole API ownership would require a
separate investigation; the command does not synthesize such rewrites or claim
the existing action durations are attainable savings.

Synthetic positive coverage demonstrates a small stable exported function plus
its private helper can form a closed candidate when it accounts for **all** of a
consumer's provider references. Git shows an independent provider implementation
edit while the group is unchanged. Zeroing the observed 35s consumer action only
reduces the 80s path to 75s because a competing branch dominates: **5s theoretical
upper bound**, not 35s savings. New package compilation cost remains unmodeled.

Tests cover grouping/closure, private incoming edges, type/method families,
interfaces, generics, embedded types, const/value atoms, initialization/state,
external function bodies, generated/tagged files, import cycles, transitive
exposure, missing history, declaration-change attribution, non-Go uncertainty and
counterfactual bounds. Read-only tests verify source/module bytes and Git status,
and detect any compiler archives written during default suggestion analysis.
