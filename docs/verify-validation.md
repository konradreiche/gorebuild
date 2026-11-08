# Verification: Prometheus remote-handler dependency inversion

## Revisions and refactor

- Repository: `/home/konrad/src/prometheus`.
- Before: `1d4faeaf10281aa16c4a5e73e4bfda92f81bc366`.
- After: `180c9be6164b076ca31081a0f013b67bfa6f8c63`.
- Separate branch: `gorebuild/verify-remote-handlers`.
- Refactor worktree: `/tmp/prometheus-verify-refactor` (clean, retained for review).

The refactor changes only `web`, API constructor/callers, and associated tests.
Remote factory calls and all four `OTLPOptions` fields now live in `web`.
`NewAPI` receives dependency-neutral lazy `http.Handler` providers. Laziness is a
small but important refinement over eagerly constructing handler values: the read
factory still runs at its old struct-initialization point, receiver factories
remain feature-gated, and the missing-appender panic still happens before write/
OTLP construction. Logger, registry, queryable, config closure, remote read limits,
accepted write protocols, lookback and type/unit/metadata options are preserved.
Tests cover all write/OTLP gate combinations, handler dispatch through registered
routes, construction order and the missing-appender error path.

The API production package no longer imports or transitively reaches
`storage/remote`. Graph comparison also removes its now-unneeded
`client_golang/exp/api/remote` import. `web -> API` remains and `web -> remote` is
added. Both complete target graphs pass Go listing and DAG cycle validation.

## Exact experiment

```sh
GOWORK=off TMPDIR=/var/tmp/gorebuild-verify-validation/tmp gorebuild verify \
  ./cmd/prometheus \
  --before 1d4faeaf10281aa16c4a5e73e4bfda92f81bc366 \
  --after 180c9be6164b076ca31081a0f013b67bfa6f8c63 \
  --package ./storage/remote --runs 3 --out prometheus-verification.json
```

The artifact is `/home/konrad/src/prometheus/prometheus-verification.json`
(about 137 MiB). It retains configuration, all correctness builds/tests, both
package graphs, every cold/warm/incremental capture with raw action evidence,
individual pairs, probe bytes/hash and summary statistics. The validation binary
and logs are in `/var/tmp/gorebuild-verify-validation/`.

Toolchain: `go1.27.3-X:nodwarf5`, linux/amd64. Both revisions use
`-mod=readonly -buildvcs=false`, `GOWORK=off`, the same environment/module-download
policy, and independently empty build caches. Correctness/test caches are never
used by benchmarks. Filesystem caches are not flushed.

Both revisions first built the executable and passed uncached tests for:

- `github.com/prometheus/prometheus/storage/remote`
- `github.com/prometheus/prometheus/web`
- `github.com/prometheus/prometheus/web/api/v1`

A separate, untimed private-cache smoke check built both binaries and ran
`GODEBUG=inittrace=1 ... --version`. Both exited successfully with identical
version output. The relevant initialization order in both was `storage/remote`,
then `web/api/v1`, then `web`. These checks do not prove behavioral equivalence
for every runtime/configuration or downstream API caller.

## Controlled input and observed execution

Each independent revision/run built cold, verified a second build executed zero
commands, then added exactly the same file to the disposable remote package:

```go
// Controlled additive compilation input; no initialization or runtime calls.
package remote

func GorebuildVerificationProbe() string { return "gorebuild-verify-input-v1" }
```

This changes compilation/export inputs without executing the added function or
initialization. It is not a representative distribution of normal remote edits.
All six probe hashes match; all six warm builds are verified no-ops. Probe
compilation is required for an observation to be accepted.

| Incremental executed work | Before | After |
| --- | ---: | ---: |
| remote Go compilation | 3/3 | 3/3 |
| API Go compilation | **3/3** | **0/3** |
| web Go compilation | 3/3 | 3/3 |
| main Go compilation | 3/3 | 3/3 |
| main linking | 3/3 | 3/3 |

This verifies the intended cache isolation for the controlled input. It does not
attribute the entire timing difference to the avoided API action.

## Individual paired times

Seconds; execution order alternates across pairs.

| Pair | Order | Cold before | Cold after | Incremental before | Incremental after | Incremental after − before |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | before, after | 136.952 | 55.056 | 11.882 | 9.150 | -2.731 |
| 2 | after, before | 73.140 | 101.588 | 14.958 | 10.217 | -4.741 |
| 3 | before, after | 111.861 | 59.484 | 11.701 | 11.452 | -0.248 |

| Metric | Before median | After median | Median paired delta |
| --- | ---: | ---: | ---: |
| Cold wall | 111.861 | 59.484 | -52.378 |
| Incremental wall | 11.882 | 10.217 | **-2.731** |
| Incremental aggregate compilation tool work | 6.056 | 5.248 | -0.703 |
| Incremental linking tool work | 5.261 | 4.418 | -0.975 |
| Incremental executed-DAG critical path | 11.544 | 9.881 | -2.728 |

Paired medians are computed from individual differences, not by subtracting
aggregate medians. Critical paths use the full observed dependency DAG, including
competing paths; these are estimates, not exact wall attribution.

All three incremental pairs were faster after, with reductions from **0.248s to
4.741s**. This is promising observational evidence, not a statistically established
2.731s causal improvement. Linking varies considerably and remains in every run.
The cold deltas span **-81.896s to +28.448s**: no dependable cold-build improvement
or absence of regression is established. Host load, filesystem/order effects and
small sample size limit inference. Avoided action durations are never summed as
wall savings.

## Preservation audit and limitations

Developer status changed only by the requested verification JSON; tracked source,
index and HEAD were unchanged. Before/after worktree listings matched, including
pre-existing registrations and the explicitly retained refactor worktree. All
verification worktrees/caches were removed; the temporary verification parent is
empty. No unrelated worktree registrations were pruned.

The global normal-cache snapshot **did change during the experiment**: 70 new
files and 875 metadata/mtime changes. New contents included unrelated `strictgo`
and `/home/konrad/src/pid` activity. Therefore a byte-for-byte global-cache
preservation claim cannot be made under the concurrent workload, and no normal
cache entries were removed or restored. Verification always passes private
`GOCACHE` values; no normal-cache path occurs in the saved artifact. Synthetic
integration tests against an otherwise empty normal cache verify it remains
unchanged. Concurrent host work is an additional timing-noise caveat.

The expensive capture used a frozen tested binary. Subsequent tested refinements
improved interrupted-add cleanup, final temporary-directory error reporting,
symlink escape checks and failure-case coverage; they do not change the experiment
or timing algorithms, and the expensive capture was not rerun for those changes.

Synthetic tests cover explicit revisions, graphs/reachability, cold-cache isolation,
warm-command rejection, probe collision/application, executed actions, paired
statistics and sparse/failed observations, correctness-build/test failures, active
cancellation, cleanup, gzip single-artifact consistency, and developer/cache
preservation. `gorebuild verify` never generates or applies refactors; this concrete
Prometheus branch was prepared separately for the requested validation.
