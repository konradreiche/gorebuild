# gorebuild CLI reference

This document contains the behavior, options, safety constraints, and measurement semantics behind `gorebuild` commands. For a quick introduction, see the [README](../README.md). For agent workflows, validation lessons and project invariants, see [AGENTS.md](../AGENTS.md).

- [Reading reports](#reading-reports)
- [`profile`](#profile)
- [`history`](#history-git-build-graphs-and-opt-in-historical-replay)
- [`explain`](#explain-diagnose-saved-rebuild-evidence)
- [`suggest`](#suggest-conditional-architectural-candidates)
- [`verify`](#verify-measure-an-existing-refactor)

## Reading reports

All commands lead with findings, then key evidence, uncertainty and the next
investigation. `--details` adds action breakdowns, dependency paths, distributions,
individual rejection reasons and diagnostics. `--top` limits displayed examples,
not saved evidence. `--out report.json.gz` saves the existing complete evidence
payload with compression; `.json` remains supported. Reports never overwrite
existing artifacts. Presentation options do not change analysis or measurements.

Terminal tables wrap at `COLUMNS` (80 columns by default), including long package
paths rather than silently truncating them. Durations use seconds to three decimal
places. A **paired wall difference** is after minus before: negative means the
refactored version was faster. It is not the difference of aggregate medians.

**Observed** means recorded execution/input evidence; **Resolved** means typed
source analysis; **Estimated** means a model; **Verified** means a controlled check;
**Inconclusive** means the proposed effect is not established. Tool work, estimated
critical paths and theoretical bounds are never measured wall-time savings.
See [reporting examples and validation](reporting.md).

## `profile`

Measures a cold build in a fresh isolated `GOCACHE`, then an unchanged warmed
build. Displays the cold critical path with duration shares and a short list of
expensive off-path actions. `-dependency-order` shows the path leaf-to-root;
`-top` limits terminal rows, not JSON data. Flags precede the target for this
command. `-out` saves the report, raw action graphs and logs in one new JSON file (`.gz` compresses);
`-json` prints the complete report to stdout.

Critical paths are dependency-chain estimates, not exact wall-clock attribution
or optimization savings. A single run is not a stable benchmark.

## `history`: Git, build graphs and opt-in historical replay

By default, combines first-parent Git change events with the **current** target's
`go list -deps` import graph, without compiling or checking out historical code.
Add `--replay N` to measure up to N historical change events in isolated temporary
worktrees. **Each sample can require a full cold build**; use a small N first.

Options may precede or follow the target:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--since` | `90d` | Positive days, UTC date, or RFC3339 timestamp |
| `--max-commits` | `0` | Optional first-parent event limit; zero analyzes the entire requested period |
| `--package` | none | Drill into a directory/local path/full import path; restrict replay sampling to its events |
| `--replay` | `0` | Opt in to up to N sampled historical rebuilds |
| `--compare-vcs` | false | Also replay each selected event with `-buildvcs=false`; doubles cold builds |
| `--out` | none | Save the complete, versioned report in one new JSON file; `.gz` compresses it |
| `--json` | false | Print the complete report as JSON |
| `--top` | `10` | Limit terminal rows and representative change events |
| `--go` | `go` | Toolchain selector for graph discovery; replay pins the selected `GOROOT/bin/go` |
| `--build-flag` | none | Repeatable flags for graph selection and replay, e.g. `--build-flag=-tags=integration` |
| `--timeout` | `0` | Deadline for the whole analysis; zero disables it |

### Coverage

The entire requested period is analyzed by default. `--max-commits` is an
explicit optional limit, not a hidden cap. Terminal and JSON reports record the
requested bounds, analyzed event count, oldest analyzed date/commit, truncation
and shallow status. A limit that equals the available event count is not
truncation. “Complete” means all available **first-parent** events in the period,
not every side-branch commit counted again. Shallow repositories are incomplete;
an empty period has no oldest event (`null` in JSON).

### Events and estimates

- One commit delta is one change event, regardless of how many files or packages
  it touches. Merge commits are compared to their first parent; side-branch
  commits are not counted again separately. Root commits are additions.
- Package identity is a repository-relative directory. Each directory contributes
  at most one change count per event. Co-change pairs likewise count once per
  event and include test changes and mapped non-Go inputs. Co-change reports
  show conditional proportions in both directions with their denominators.
  Small counts, even 1/1, are not evidence of architectural coupling. Full file
  paths and commit IDs remain in JSON.
- Non-test `.go` changes and mapped potential build inputs in the current
  target closure seed a potential
  invalidation set: the changed packages plus their transitive consumers.
  Overlapping sets are unioned **per event**. Package rows are overlapping views,
  not independent build scenarios, and must not be summed as developer time.
- Static rankings use historical **build-event frequency**, with directory
  names breaking ties. Reverse-dependency reach is supporting information, not
  a ranking factor or a cost estimate. There are no tiers or combined impact
  scores. Frequency alone does not imply an optimization opportunity.
- Package drill-down includes monthly activity, potential consumers, an
  **unweighted** longest path to the target, and recent change-event evidence.
  No compilation times, median/P90 latencies, or expensive-path timings are
  available without measurements.

### Limits and safety

The current graph does not prove historical invalidation. Export-data caching,
compiler settings, build constraints, dependency upgrades and historical source
layout can change what actually recompiles. Test-only changes are excluded from
static compilation reach and package build-event cohorts, not from Git
frequency/co-change counts or event-level replay evidence. VCS stamping can cause
linking even after a test/documentation-only change.

Non-Go files are classified before attributing potential invalidation:

- **Potential build input**: current embedded/native inputs, matching embed
  patterns (including removed assets), native/assembly/generated input filenames,
  and build/dependency configuration. Mapped inputs count once per package/event;
  config-wide or unmapped inputs do not invalidate the whole target by assumption.
- **Unlikely**: documentation, CI and test data not matching current embed inputs.
- **Unknown**: files with no reliable current input mapping or filename rule.

These are conservative, current-graph/filename heuristics, not reconstructed
historical generator or embedding rules. Current embed inputs override filename
heuristics, so an embedded README is not dismissed as documentation. JSON retains
per-file classification/reasons/owners and file-change/event counts for all three
categories (an event can appear in multiple categories; a path changed twice
counts as two file changes, not one unique file). Unknown and unmapped
potential files remain in `unmodeled_files`, not zero-cost build estimates.

Deleted, renamed and out-of-target Go directories remain in the report but are
not assigned invented import paths or rebuild costs. Directory moves are not
stitched into a single package history.

Graph discovery uses a temporary isolated Go cache and defaults to
`-mod=readonly` to avoid repairing module files. For vendored dependencies,
explicitly use `--build-flag=-mod=vendor`; `-mod=mod` is rejected. The effective
flags, toolchain, environment, HEAD, dirty status and history bounds are recorded.
Module downloads are shared. Dirty and shallow checkouts are flagged; shallow
boundary commits may appear as root additions. The checkout/index/HEAD are not
switched or reset. Output files are never overwritten.

### Measured historical rebuilds

For every selected event, replay:

1. Creates a detached temporary worktree at the first parent and a **fresh**
   isolated build cache. Baseline/list preflight uses a separate cache.
2. Performs a cold parent build, then a warmed parent build. A baseline that
   still executes commands is excluded, not accepted as a no-op.
3. Checks out the event commit in the **same** worktree, removes temporary
   untracked/ignored files there, and builds with the **same** cache, output path,
   Go toolchain and configuration.
4. Captures executed compilation/link actions, wall time, recorded tool work,
   raw action graphs and logs. Removes only its own temporary worktree, including
   after build failures or cancellation.

Sampling is deterministic and does not just take the latest N commits. It first
selects the chronologically newest/oldest eligible events when the budget permits
(N=1 selects the newest), then round-robins four ordered queues:

- Frequent current-target packages, one occurrence per package per round.
- Broad-reach current-target packages, likewise visiting each package in turn.
- Multi-package build events, largest changed-package sets first.
- Chronological quantile anchors, then remaining chronological events.

Queues share one sample for duplicate events; no score combines the criteria.
Before repeating nearly identical coverage, prefer different changed-package and
input-class sets within four chronological bands. Once distinct cohorts are
exhausted, fill the budget with repeated cohorts and record that fallback.
With a tiny budget not every criterion can be represented. Ties are deterministic,
execution follows first-parent log order, and every sampled event records its
selection reasons. All events, including test/documentation changes, remain
eligible for temporal coverage; `--package` restricts candidates to events
attributed to that directory. Failed/excluded samples are **not replaced** with
successful ones. Root/shallow-boundary events without a parent, active
workspaces, external local module replacements, external `GOCACHEPROG` caches and
path-dependent overlay/modfile/pkgdir/toolexec configuration are explicitly
excluded. External cache programs must be disabled to ensure replays cannot
benefit from an unrelated earlier build.

Configuration changes are reviewed against the **historical** selected main
module, dependency modules, declared local replacements and package directories,
both before the parent build and before the child rebuild. A relevant `go.mod`,
`go.sum`, active vendor manifest or applicable toolchain-selection file causes
exclusion. A new module boundary above a selected package is relevant too.
Unrelated nested modules such as documentation examples do **not** cause an
exclusion merely because a file is called `go.mod`. With `GOWORK=off`, changes to
workspace files are unrelated to this fixed configuration. Failed relevance
checks are explicitly `uncertain`, never silently treated as unrelated. Every
review records path, revision, relevance and reason in the artifact. Unbuildable parent/commit revisions are
recorded as failures, with phase and diagnostics. Prefer `GOWORK=off` and a
self-contained module for replay. Unsupported historical Go versions fail rather
than silently switching toolchains: automatic toolchain switching is disabled.

Measured reports retain build-event frequency as their primary ordering and use
median latency only to break frequency ties. They show the **median whole-event
rebuild wall time** of successful build-event samples involving each package,
and successful replays / historical build events. Test-only/no-op events remain
in event evidence and global totals but do not dilute package build-event cohorts.
Medians need at least two successful replay observations; a single measurement
remains in event evidence but does not become a package-cohort median.
Drill-down adds range (at least two samples), nearest-rank P90 (at least ten),
selected/failed/excluded counts, tool-work medians and sampled-event evidence.
Unavailable distributions stay `null` rather than implying false precision. These are
observational, overlapping **event cohorts**, not independent package timings or
causal attribution. A commit touching five packages is measured once; its cost
is not allocated five times or turned into a developer-time-loss score. JSON's
`total_measured_wall_time_ns` sums successful events once. Unsampled, failed and
excluded events never become zero-duration measurements; unavailable statistics
are JSON `null` / terminal `-`.

### Explaining executed work

The default measured report distinguishes **modified packages** and their
whole-event wall-time cohorts from **actually rebuilt packages** and the median
recorded tool work of their executed build actions. It also shows median warm
no-op baseline time and the most expensive whole events, with compilation work,
link work and an executed-only critical-path estimate. Warm baseline time is
context, not a value subtracted from rebuild wall time. These are not additive
portions of wall time or isolated package rebuild latencies.

`--package` drill-down shows each sampled event's changed files, warm baseline,
executed action and recognized compiler/cgo/assembler/link command counts,
rebuilt packages, and largest critical-path contributions plus off-path executed
work. Full dependency-order paths, all individual actions, both elapsed and
recorded tool durations, and commands remain in JSON.

History reuses `profile`'s DAG analyzer. Executed-only critical paths traverse
the **full** dependency graph, assigning zero weight to cache/synthetic nodes and
measured elapsed time to executed nodes. Removing cache nodes outright could
break real dependency chains. The path is not a sum of the slowest actions and
is not necessarily the scheduler's wall-clock bottleneck. Go startup, VCS probes,
cache lookups and other overhead outside executed actions are not attributed.

An action needs a recorded `Cmd` to count as executed; `NeedBuild` or a long cache
check is not evidence of compilation. Cgo/assembly-bearing action counts are
**overlapping subsets**, not extra independent actions. Recognized commands use
unambiguous tool executable names; wrappers/native compilers are not guessed.
Go may record several commands within one action with **one aggregate CmdReal**;
we retain that action's aggregate work, never invent per-command durations or
sum its cgo/assembly components again. Recompiled packages (recognized Go
compiler commands) are recorded separately from executed package build nodes.

### Optional VCS-stamping comparison

The shared build-flag mechanism supports standalone `--build-flag=-buildvcs=false`
runs. Sampling is independent of this flag; compare commit IDs and history bounds
when contrasting captures taken at different times.

`--replay N --compare-vcs` makes paired measurements in **one artifact**, using
normal Go VCS defaults and `-buildvcs=false`. Each configuration gets a separate
fresh worktree/cache/output, cold parent and verified warm parent. The second
configuration never borrows compilation artifacts from the first. Execution
order alternates by sampled event and is recorded. Do not combine this mode
with explicit `-buildvcs` flags/GOFLAGS: its normal configuration must retain
Go's default behavior.

Both outcomes, flags, action graphs, critical paths and signed differences
(**no-VCS minus normal**) are retained. The comparison summary uses only matched
successful pairs; failures/exclusions for either configuration stay visible.
It compares event wall time, warm baseline, compilation/link tool work and action
counts. Differences are **observed configuration contrasts**, not automatic VCS
cost attribution: machine load, filesystem cache, order and ordinary measurement
noise still matter. Source changes can require linking even with stamping off.

History artifacts use schema version 4 and retain the complete static
analysis, all selected replay outcomes, baseline/warm/rebuild action graphs and
logs in **one** file. On cancellation during replay, `--out` preserves partial
evidence before the command reports an error. Without `--out`, cancelled runs do
not print partial JSON. Small coverage, single-build variability, machine load,
external native dependencies and survivor bias limit conclusions; medians are
not stable benchmarks or predictions of refactoring savings. Builds execute
historical build inputs (including native tools): replay only **trusted**
repositories; worktree/cache isolation is **not a security sandbox**.

See [Prometheus validation observations](history-validation.md) for an
actual matched 20-event comparison, action/path checks, and measurement limits.

## `explain`: diagnose saved rebuild evidence

```sh
gorebuild explain --history prometheus-history.json --commit 503180645ef0
gorebuild explain --history prometheus-history.json --package ./web/api/v1
# Optional current-source inspection of one direct consumer relationship:
gorebuild explain --history prometheus-history.json --package ./web/api/v1 \
  --inspect-consumer ./web --source /path/to/prometheus
```

The default command is offline/read-only: no Git checkout, Go invocation or
build runs. It accepts `.json` and gzip-compressed JSON (including `.json.gz`),
streams events rather than retaining cold graphs/logs, and reanalyzes recorded
rebuild DAGs with the shared profiler instead of trusting stored derived totals.
The existing history artifact remains unchanged; no new large report format is
introduced. Use `history --out capture.json.gz` or compress a capture externally.
Corrupt graphs are reported as unusable, never zero-cost observations.

Select exactly one of `--commit` (full hash or unique prefix across all recorded
events) and `--package` (import path or directory relative to the captured
repository, independent of the caller's working directory). `--top` defaults to
5 and controls expensive actions, examples and declaration details.

Event explanations show changed inputs, executed nodes/packages, warm baseline,
wall time, recorded compilation/link work, the actual weighted dependency chain,
its largest elapsed contributions, recurrence across other sampled commits, and
available paired VCS evidence. Package explanations count **actual recognized Go
compiler executions per event**, show co-occurring changes and historical DAG
routes, and separate package-node tool work from whole-event build latency.
Co-changing cohorts and repeated package nodes are never counted as independent
builds. Dependency paths suggest possible propagation, not declaration-level
causality. Sparse observations remain explicitly labeled.

Counterfactuals zero **one executed node's elapsed duration**, keeping all edges,
other weights and zero-weight cache nodes intact. The shared DAG algorithm
recomputes the longest path, including competing paths. The reported maximum
modeled improvement is an optimistic theoretical upper bound on this fixed
critical-path model, **not predicted achievable wall-clock savings**. Off-path
or competing-path actions may yield no modeled improvement.

`--inspect-consumer` requires `--package` and an explicit `--source`. It uses
`golang.org/x/tools/go/packages` with the captured platform, environment, build
flags/tags and module/workspace settings, resolving actual imported types rather
than fabricating opaque packages. Only the provider and consumer are load roots;
required imports use compiler export data or source type checking, without
loading unrelated repository packages or retaining every dependency's AST.
The current source revision, actual/captured toolchains and completeness are
reported. Captured path-dependent overlays/modfiles/pkgdir/toolexec settings
are rejected rather than silently relocated.

By default this Go invocation uses **`-n`**: compilers do not run. Missing export
data may be resolved by go/packages' source fallback. If generated/cgo/export
data remains unavailable, analysis explicitly reports diagnostics/incompleteness.
`--allow-export-builds` separately permits compiling dependency export data when
needed. Neither mode links the target, runs generators or executes initialization.
Go may resolve/download modules and cache module indexes; analysis uses read-only
module selection unless captured vendor selection applies.

The pair must have co-recompiled in recorded evidence and the current consumer
must directly import the provider. Output combines consumer recompilation counts,
historical changed-package routes, resolved exported declaration/field/method
uses, and internal declaration closures including unexported helpers. Initialization,
package state, generated files, methods/interfaces, pragmas and cycles constrain
narrowing. Implicit interface satisfaction, dynamic dispatch and reflection are
not a complete runtime call graph. Even complete type checking is **not** proof
of cache-invalidation causality or removability. No refactoring is proposed.

Use `--out inspection.json` to preserve all referenced declarations, reachable
provider declaration edges, full diagnostics and constraints in a small selected
explanation artifact, rather than dumping graphs/errors to the terminal. Existing
files are never overwritten; the original history artifact is never changed.

### Explicit cache-hash replay

```sh
# EXPENSIVE: explicitly requests a cold parent + warm verification + child build.
gorebuild explain --history prometheus-history.json --commit 503180645ef0 \
  --replay-cache ./web/api/v1 --source . --out api-cache.json
```

This opt-in mode reuses history's isolated worktree/cache replay and configuration
exclusions, requiring the captured Go version. It enables `GODEBUG=gocachehash=1`
and **filters logs during collection** to the selected package and original target
main build/link blocks. No unrelated hash blocks or full module-index diagnostics
are retained. It compares warmed-parent/child hash inputs: Go source, other/embed/
native files, imported content hashes, build/tool/environment inputs, and VCS/
module metadata and linking. Full selected logs and categorized differences go
in `--out`; terminal output remains bounded. Missing/internal-format evidence,
failures and exclusions are explicit. These are **new diagnostic observations**,
never replacements for the original timings. Cache-key differences are stronger
than reverse-dependency guesses, but do not establish declaration-level causality
or prove the sole reason for a cache miss. Generators are not run, so generated
input provenance remains unverified.

## `suggest`: conditional architectural candidates

```sh
GOWORK=off gorebuild suggest ./web/api/v1 --history prometheus-history.json
# Save complete groups, rejected proposals, source configuration and event bounds:
gorebuild suggest ./internal/query --history app.json --source . --out suggestions.json
```

Recommendations only: no source edits, historical replays, target builds or links.
Source defaults to the current repository root directory (`--source .`); package
queries remain relative to the captured repository. `--consumer` restricts analysis
to one direct repository consumer; otherwise consumers come from the captured
target graph. Go loading uses the same configuration-aware, compilation-disabled
`-n` pass as `explain`. `--allow-export-builds` explicitly permits export-data
compilation if necessary. Unrelated packages/tests are not automatically loaded.

Suggestions are organized into three evidence-backed categories:

- **A. Reduce incoming dependencies:** maps each actual target import to the
  declarations/symbols requiring it, counts relevant historical source changes
  and measured target compilations without target source edits, and checks
  critical-path presence. It evaluates closed import-dependent extractions and
  narrowly supported factory-result inversion. A wrapper that leaves
  `target -> adapter -> dependency`, an alternate transitive route, or a cycle
  is rejected. Neutral-result inversion requires complete typed constructor call
  sites in an existing consumer, dependency-free interface results, and movable
  construction inputs; arbitrary interface rewrites are not synthesized.
- **B. Reduce outgoing dependencies:** preserves the existing consumer-isolation
  analysis and its rejected candidates.
- **C. Reduce compilation complexity:** reports measured tool work and a source
  inventory (large declarations, generated files, generic instantiations).
  These are investigation areas, not compiler-cost attribution. **Compiler
  profiling is required** before recommending specific optimizations.

```sh
# Read an existing selected cache replay; this does not run another build.
GOWORK=off gorebuild suggest ./web/api/v1 --history prometheus-history.json \
  --cache-evidence prometheus-cache.json --out prometheus-suggestions-cache.json
```

`--cache-evidence` accepts a selected `explain --replay-cache` JSON artifact.
It validates package/commit/configuration compatibility and recomputes input
changes from raw hash blocks rather than trusting saved differences. Only target
compilation inputs—not main/linking inputs—support target reasoning. Partial,
ambiguous or mismatched evidence is reported, not treated as a proven cache cause.
A target source edit or observed target-file hash change never becomes an
avoidable compilation. Conditional bounds retain competing paths and exclude
other retained imports/configuration inputs that remain exposed to the change.
Measured actions, observed cache inputs, resolved types, modeled graph changes
and unverified refactoring assumptions remain separate in terminal and JSON.

The outgoing analyzer starts from resolved consumer references and computes cohesive
provider declaration closures. Types, receiver methods and fields move as atomic
families; shared value specs and const/iota blocks remain together. It checks
unexported incoming references, state/initialization, generated code, directives,
build variants, external bodies, proposed internal destinations and import cycles.
A group is **rejected** if consumer references would retain the provider import,
if another transitive route retains exposure, or if it is disconnected or a broad
package-sized closure rather than a bounded extraction (40-declaration scope).
Rejected groups get no fabricated benefit estimate. This is deliberately not an
exhaustive optimizer: arbitrary interface redesign, wrappers and cross-package
rewrites are not synthesized to rescue rejected groups. Incoming factory-result
inversion is a separate, explicitly constrained strategy, not a reclassification
of outgoing rejections.

For structurally supported groups, Git blob/AST fingerprints compare the selected
declarations in first-parent historical snapshots. Import/directive changes are
conservatively included; unavailable, ambiguous or absent declarations are unknown,
not "stable". These are syntactic observations, not historical type checking.
Measured consumer recompilations are counted per event, never summed cohorts.
Conditional avoidance excludes changed groups, direct consumer edits, exposed
upstream/other import inputs, unmodeled non-Go inputs and stamped main actions.

Each eligible event recomputes the original executed DAG with **one consumer
compilation action zeroed**, preserving competing paths. This is a conditional
fixed-DAG upper bound, **not predicted savings**: the new package's cost, cache
behavior, initialization semantics and actual before/after performance remain
unverified. No downstream actions are automatically zeroed. Sparse estimates are
labeled; events never become zero-cost observations when evidence is missing.

Status is `requires validation`, `requires investigation`, or `rejected`—never
"verified safe". Ranking uses structural support first, conditional measured-event
coverage and critical-path presence next, then bounded group size; no opaque score.
`--top` limits terminal output only; `--out` saves all edges, constraints, rejected
groups, Git observations and per-event theoretical bounds without overwriting files.
Source edits remain manual; `gorebuild verify` measures explicit existing before/after revisions.

## `verify`: measure an existing refactor

```sh
GOWORK=off gorebuild verify ./cmd/prometheus \
  --before BASELINE_COMMIT --after REFACTORED_COMMIT --package ./storage/remote \
  --runs 3 --out prometheus-verification.json
```

**Explicit, potentially expensive builds and tests.** Both revisions are required
and resolved to immutable commits. This command never generates/applies a
refactoring or changes the developer checkout. It uses only its own detached
worktrees/private build caches; module downloads are shared. Only
verify trusted repositories: builds/tests execute code and are not sandboxed.
Use `TMPDIR` on a filesystem with ample space for large targets.

Before benchmarking, each revision must build and pass correctness tests. By
default these cover changed Go packages inside the target closure plus the
controlled `--package`; repeat `--test-package` to select checks explicitly.
Expanded mode also inventories tracked Go import/selector callers, including tests
and build-constrained files outside the executable graph. Discovered outside-graph
caller packages are tested in the selected configuration; the lexical census is
not a whole-repository type proof. Other platforms/tags and external consumers
remain explicitly untested. `--race-tests` enables untimed race correctness checks;
`--runtime-arg=--version` requests an executable smoke check with initialization
traces. Opt-in `--after-test-overlay path/new_test.go=local-file` supplies additional
new test files only to after-revision correctness worktrees, never benchmarks;
complete overlay content/hash is saved. No production refactoring is generated.
Passing tests is **not behavioral-equivalence proof**. Correctness failures,
module/toolchain mismatches, unsupported external replacements/workspaces,
build/cache/probe errors and cleanup failures invalidate performance comparisons.
A single new `--out` artifact retains failure diagnostics and any partial captures.
Existing files are never overwritten; `.json.gz` compresses the same evidence.

Graph comparison records all added/removed imports and repository-package
reachability to the controlled dependency. Successful `go list` plus DAG validation
checks cycles. The primary experiment pins the selected Go toolchain, platform,
flags/environment, read-only module mode and **`-buildvcs=false`** across revisions.
Path-dependent tool wrappers/overlays and build-forcing/output flags are rejected.

The default scenarios are `implementation,exported`; select with `--scenario`:

- **Implementation:** insert a same-line empty block in an existing non-generated
  function whose source is identical in both revisions. Exported declarations and
  executable behavior are unchanged. This changes a compilation input, but may
  leave downstream packages cached; no propagation is assumed.
- **Exported:** add the same unused exported probe function, without initialization
  or runtime calls. Name/file collisions are rejected.
- **Historical:** add `--historical-commit REF` (repeatable), or `--history capture.json`
  to select one suitable event automatically. Source files must have identical
  preimages in both experiment revisions. Only existing Go-file modifications in
  the selected package are supported. Forward/reverse applicability is checked
  in both worktrees; inverse edits are explicitly labeled, not called the original
  historical event. Multi-package/config/test-file events are rejected unless
  `--historical-subset` explicitly permits a documented package-only subset.
  Root/merge events, conflicts and unsupported inputs are reported. Real patches
  may change behavior; edited-revision tests must pass before timing.

Every scenario's exact content/patch, hashes and provenance are saved. No unrelated
source/package is edited. Scenarios are correctness-tested on both revisions with
separate correctness caches before benchmark preparation.

`--cold-runs` (default 3) controls independent empty-cache cold pairs, **separate**
from `--runs` (e.g. 10) incremental pairs per scenario. One verified warmed baseline
per revision is retained as an immutable cache/output template. Each incremental
arm uses an independent **byte copy, never hard links**, restoring the same source
and baseline output paths, then verifies a zero-command warm build before applying
its frozen edit. Copying/preparation is untimed. The edited build must actually
execute the selected package's compiler action. Templates, individual copies and
worktrees are private and cleaned up after completion, errors or cancellation.

`--same-revision-control` defaults true: two independent copies of the **before**
revision receive the same scenario, providing a same-revision timing-noise control.
Treatment and control pair order alternate and their group order is bracketed
across runs; the complete schedule is retained. These controls do not eliminate
all host/filesystem variability.
Each build retains wall time, raw action graph, executed compiler/link actions,
aggregate tool work and the shared executed-DAG critical path, including competing
paths. Compiler/link package sets are observations, not reverse-dependency guesses.

Reports keep cold and incremental results separate, retain individual paired runs,
and show medians/ranges of **paired after-minus-before deltas**, not differences
of aggregate medians. Medians require at least two valid observations. Deltas
spanning zero are explicitly inconclusive; even consistent small-sample results
are observational, not statistical significance. OS filesystem caches and host
load are uncontrolled. Avoided compilation work is never summed as wall savings.
Structural per-package execution counts are reported separately from timing.
"Preliminary" requires more observations; "Inconclusive" includes mixed-sign
paired deltas or effects within observed controls. "Consistent (observed only)"
requires at least five treatment/control pairs, same-sign treatment differences,
and median magnitude above the maximum observed absolute control difference.
This transparent descriptive rule is **not a statistical-significance test**.

The concrete Prometheus refactor, constructor/gate checks, three-pair measurements
and preservation audit are documented in `verify-validation.md`. Expanded
scenario/control validation is documented in `verify-expanded-validation.md`.
