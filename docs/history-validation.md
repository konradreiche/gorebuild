# Historical rebuild validation: Prometheus

Captured sequentially on 2026-11-09 with `go1.27.3-X:nodwarf5`, linux/amd64,
`GOWORK=off`, at Prometheus HEAD
`1d4faeaf10281aa16c4a5e73e4bfda92f81bc366`.

```sh
GOWORK=off gorebuild history ./cmd/prometheus --since 90d --replay 20 \
  --out prometheus-history.json
GOWORK=off gorebuild history ./cmd/prometheus --since 90d --replay 20 \
  --build-flag=-buildvcs=false --out prometheus-history-novcs.json
```

Both commands exited successfully. Each analyzed **477 events**, selected the
**same 20 commit IDs**, measured **18**, failed **0**, and excluded **2** relevant
root `go.mod` changes. Both artifacts are single JSON files in the Prometheus
repository (about 337 MiB each). The unrelated remote-storage example module
change was replayed, with parent and child configuration reviews retained.

## Observations

| Successful event measurements | Normal | `-buildvcs=false` |
| --- | ---: | ---: |
| Median rebuild wall | 12.581s | 13.780s |
| Median warm no-op wall | 0.427s | 0.348s |
| Median compilation tool work | 10.729s | 11.189s |
| Median linking tool work | 3.124s | 3.037s |
| Median executed critical-path elapsed time | 12.208s | 13.518s |
| Total executed build nodes / recognized Go compiler commands | 327 | 324 |
| Total executed link nodes | 18 | 15 |
| Total executed nodes | 345 | 339 |

No incremental cgo/assembly commands were observed in these samples. Each cold
parent still executed build commands, and every warmed parent executed zero
commands. Every executed-action detail matched a raw graph node with a recorded
`Cmd`; executed critical-path weights summed to their recorded estimate and
nonzero weights belonged only to executed nodes.

Three events compiled/linked `cmd/prometheus` normally but performed no
compilation/linking with stamping disabled:

| Commit | Change | Normal wall | No-VCS wall |
| --- | --- | ---: | ---: |
| `1d4faeaf1028` | GitHub workflow | 5.514s | 0.483s |
| `6e69e843b38d` | Unrelated example module `go.mod`/`go.sum` | 6.766s | 0.342s |
| `2b2c984026ab` | Histogram test file | 8.744s | 0.277s |

The other **15 events still linked** with stamping disabled. Across all 18
matched events, the median paired wall difference (no-VCS minus normal) was
**+0.101s**. The difference of the two overall medians is a different statistic.
These sequential, single-build captures do **not** demonstrate an overall speedup
from disabling stamping; timing noise and order effects remain visible.

## What the original similar package medians were hiding

The normal run rebuilt `cmd/prometheus` in all 18 successful events, and both
`web/api/v1` and `web` in 15. Their median individual recorded build-action work
was 2.317s, 2.819s and 2.232s respectively—not 12–17 seconds of isolated package
latency. Changed-package medians describe overlapping whole-event cohorts.

For example, normal event `503180645ef0` took 19.263s wall, with 35.909s aggregate
compilation work, 4.578s linking work and an 18.845s executed critical-path
estimate. Its actual dependency chain runs from `model/histogram` through the
TSDB/storage packages to `web/api/v1`, `web`, `cmd/prometheus`, then linking.
The largest elapsed contributions were linking (4.768s), `web/api/v1` (3.647s),
`cmd/prometheus` (3.107s), and `web` (3.085s). This is one event, not a stable
benchmark or a prediction of optimization savings.

Tool work can overlap across actions and exceed wall time. Action elapsed time
is different from recorded tool time; critical-path estimates are not additive
with either. The evidence supports investigating shared downstream compilation
and linking, not treating each changed package's event median as its own cost.
No architectural recommendations or causal savings estimates follow from these
small cohorts.

## Reproducibility and limits

The two captures used the same frozen validation executable, identical selected
commits and independent fresh caches. Small follow-up refinements for nested-target
ancestor/vendor configuration scope and cold-output validation are covered by
unit/integration tests; these expensive captures were not repeated for those
refinements, which do not change the module decisions or action analysis for
this root-module, non-vendor target. Final source validation includes
`go test ./...`, `go vet ./...`, race tests and `strictgo ./...`.

The separate captures have slightly different requested start timestamps but the
same available events and samples. For a single fixed-window paired experiment,
use `--compare-vcs`; it alternates configuration order and stores both complete
outcomes and differences in one artifact. Repeated measurements would be needed
before making stronger timing claims.
