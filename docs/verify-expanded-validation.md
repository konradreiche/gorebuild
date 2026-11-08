# Expanded verification: Prometheus results

This extends the three-pair experiment in `verify-validation.md`. Structural cache
isolation and end-to-end timing conclusions are deliberately separate.

## Reproducible inputs

- Baseline: `1d4faeaf10281aa16c4a5e73e4bfda92f81bc366`.
- Refactor: `180c9be6164b076ca31081a0f013b67bfa6f8c63`.
- Target: `./cmd/prometheus`; edited package: `./storage/remote`.
- Selected Go toolchain/platform/environment and `-mod=readonly -buildvcs=false`
  are shared across all arms. No source rewriting/refactoring is generated.

Scenarios retain exact bytes/patches and SHA-256 hashes:

1. **Implementation-only:** insert a same-line empty block in an existing,
   non-generated function whose original file is identical in both revisions.
   Exported declarations and executable work do not change. The selected package
   must actually compile, but downstream package propagation is not assumed.
2. **Exported declaration:** add the same unused exported probe function, without
   initialization or calls. A name/file collision is unsupported, not overwritten.
3. **Historical:** select an existing isolated, applicable source event from
   `prometheus-history.json`. The chosen event is
   `d0aa81d134171b6516241b4ff9538d32d7a92f60`, modifying only
   `storage/remote/write.go` (`WriteStorage.ApplyConfig` queue bookkeeping).
   Forward application is already present at both selected revisions; the
   equivalent **reverse patch** is used and explicitly labeled. This is an inverse
   historical edit, not a timing replay of the original commit. Behavioral
   equivalence is not assumed; edited-revision correctness tests are required.

A multi-package/config/test-file event is rejected unless an explicit package
subset is permitted. A subset retains all original event paths and is never
called an isolated historical event. Missing parents, merges, additions/deletions,
conflicts, nonidentical preimages and unsupported source are reported explicitly.

## Timing controls

Cold benchmarking uses three independent empty-cache pairs, separately from ten
incremental pairs per scenario. Cold source paths are stable and no source edit
is applied. The first verified warm cache/output per revision becomes an immutable
baseline template. Each incremental arm receives independent byte copies of both
cache and baseline output; no hard links are used. Copying/preparation is untimed.
Source is restored only in disposable worktrees at the same paths. Every copied
baseline must warm with zero executed commands before its frozen edit is applied.

The restored binary matters: an incremental executable left by the preceding
scenario must not make the next baseline relink. A synthetic test caught this
preparation issue before the real benchmark; templates restore both cache and
output bytes. Every edited build must compile the controlled package.

Each scenario has before/after treatment pairs and **before/before** same-revision
control pairs. Both arm order and treatment/control group order alternate. All
individual commands, package sets, critical paths, inputs and schedules are saved.
Same-revision controls estimate observed timing noise; they do not remove
filesystem, scheduling, memory-pressure or other concurrent host effects.

Descriptive timing labels have explicit rules, not confidence scores:

- **Preliminary:** fewer than five valid treatment/control pairs, or insufficient
  observations for a median.
- **Inconclusive:** treatment paired deltas span zero, or treatment median
  magnitude does not exceed the maximum absolute same-revision paired difference.
- **Consistent (observed only):** at least five treatment/control pairs, all
  treatment deltas have the same sign, and treatment median magnitude exceeds
  the maximum absolute control difference. This is not statistical significance.

Cold variability and possible regressions are reported independently. Avoided
compiler time and estimated critical paths are not summed as wall-clock savings.

## Correctness scope

Both revisions must build/pass selected tests before any timing. Every edited
revision/scenario is tested again with correctness-only caches. Expanded validation
also requests `--race-tests`, a `--version` runtime smoke check with initialization
traces, and an explicit after-only `_test.go` overlay. The overlay is retained in
the artifact with content/hash and is never installed in benchmark worktrees.

The supplemental test checks all write/OTLP feature combinations, exactly-once
provider invocation per API instance, independent construction across instances,
concurrent registered-route access, and read-construction failure ordering.
Existing refactor tests cover registration and missing-appender ordering.

A tracked-source lexical census records imports/qualified references to packages
whose dependency edges changed, including tests and constrained files outside
the executable graph. Discovered out-of-graph caller packages are tested in the
selected configuration. Dot imports, reflection, generated/untracked code, external
modules and inactive platform/tag configurations remain explicit limitations.
Only the effective linux/amd64 configuration is exercised; passing builds/tests,
race checks and smoke checks do not establish complete behavioral equivalence.

## Artifact and operational audit

The expanded artifact is `prometheus-verification-expanded.json` in the Prometheus
checkout. Captures retain a canonical raw action graph plus derived summaries,
without duplicating the full typed graph. Logs and the frozen validation binary
are under `/var/tmp/gorebuild-verify-expanded/` and
`/var/tmp/gorebuild-verify-validation/`.

The ten-pair validation completed successfully (exit 0), using the existing revisions and adding
`--history prometheus-history.json --race-tests --runtime-arg=--version` and the
explicit correctness-only lifecycle test overlay. No module generation, new
refactor, or source change in the developer checkout is requested. Module downloads
are shared; worktrees and all compiler/test/cache-copy state are private.

## Observed results

All three scenarios completed **10 treatment pairs and 10 same-revision control
pairs**: 120 measured incremental arms, all with verified zero-command warm
baselines. All six cold arms also passed zero-command warm checks. Both original
revisions and all six edited revision/scenario combinations passed race-enabled
correctness tests. Both runtime smoke checks succeeded; full initialization traces
are retained, but no general initialization-equivalence claim is made. The caller
census found the existing `web` caller in each revision and no out-of-target caller
package requiring additional tests.

| Scenario | API compiler before/after | Paired incremental wall median [range] | Control absolute paired median / maximum |
|---|---|---|---|
| Implementation-only | 0/10 → 0/10 | +0.393s [−4.318s, +7.150s] | 0.710s / 6.411s |
| Exported declaration | 10/10 → 0/10 | −0.963s [−12.912s, +22.428s] | 3.684s / 14.720s |
| Inverse historical edit | 10/10 → 0/10 | −2.061s [−6.472s, +9.054s] | 4.964s / 19.605s |

**Every latency conclusion is inconclusive.** All treatment ranges span zero and
observed same-revision variability exceeds the treatment medians. The earlier
three-pair speedup did not become a reliable ten-pair latency result on this host.

The structural result is stronger and scenario-specific: API no longer reaches
remote; API compilation was avoided in all 20 exported/historical treatment pairs.
The implementation-only edit compiled remote but left API, web and main cached in
**both** revisions, with no link execution. This is not an avoided API action.
Remote compiled in every scenario/arm. Web/main compilation and linking remained
necessary in both revisions for the exported/historical edits.

Cold before/after medians were 96.549s / 104.880s; paired after-minus-before median
**+12.104s**, range **−0.756s to +30.825s**. Only three cold pairs were collected;
they do not support a cold speedup and retain possible regressions explicitly.

Independent artifact checks recomputed paired medians from individual observations
(with sub-nanosecond midpoint rounding tolerance), checked each package execution
set, same-revision control commits, zero-command warm graphs and frozen input
hashes. Developer Git status changed only by the new 733 MiB evidence artifact;
worktree registrations were byte-for-byte unchanged, including unrelated prunable
registrations. The private temporary parent was empty after cleanup. No normal
cache entry was removed/restored or used as an experiment cache. A global live-cache
snapshot is not claimed: other processes can still change that shared cache.

The frozen expensive capture preceded a later cold-only summary interpretation
fix and help-text clarification; no measurement algorithm changed, and the real
cold range already spans zero. These changes are covered by the final test/vet
checks without repeating expensive target measurements.
