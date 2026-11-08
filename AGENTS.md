# Working on gorebuild

This file is for coding agents and contributors working **in the gorebuild repository** or using `gorebuild` to investigate another Go repository. The [README](README.md) is the human-facing introduction; the [CLI reference](docs/cli-reference.md) holds detailed flags, output semantics, and limitations.

## Objective

Help developers identify expensive Go builds and recurring incremental rebuilds, explain them with evidence, find plausible architectural improvements, and verify whether a change actually helps. **Do not optimize for impressive numbers or a positive recommendation.** A supported rejection, an avoided compile action without a wall-time win, or an inconclusive result is a useful outcome.

## Documentation and evidence map

Read the documents relevant to the change before modifying code or interpreting a saved result:

| Document | Why it matters |
| --- | --- |
| [README.md](README.md) | User journey, installation and short command examples |
| [docs/cli-reference.md](docs/cli-reference.md) | Complete command behavior, flags, cache/replay semantics and constraints |
| [docs/reporting.md](docs/reporting.md) | Reporting examples and validation of the conclusion-first presentation |
| [docs/history-validation.md](docs/history-validation.md) | Real Prometheus Git replay, VCS-stamping comparison and measurement limitations |
| [docs/explain-validation.md](docs/explain-validation.md) | Offline rebuild explanations, typed consumer inspection and competing-path counterfactual validation |
| [docs/suggest-validation.md](docs/suggest-validation.md) | Observed cache-input changes, outgoing extraction rejections and invalidation-causality limitations |
| [docs/dependency-boundary-validation.md](docs/dependency-boundary-validation.md) | Incoming dependency analysis, historical evidence and the supported remote-handler factory-inversion proposal |
| [docs/verify-validation.md](docs/verify-validation.md) | Initial Prometheus remote-handler refactor, three-pair benchmark, correctness and preservation audit |
| [docs/verify-expanded-validation.md](docs/verify-expanded-validation.md) | Multi-scenario verification, same-revision controls and inconclusive timing results |

The validation documents are **case studies**, not universal performance claims or acceptance thresholds. When revising behavior described in one of them, update the relevant documentation and explain whether old captures are still representative. This package includes updated documentation files; the other linked `docs/` files are maintained in the main repository and are not recreated here.

## Workflow when using gorebuild on another repository

1. Identify the main Go build target and relevant toolchain, build tags, module/workspace configuration, and working-tree state. Do not silently change the target or configuration to make a run succeed.
2. Run `profile` to establish cold-build cost and a warm unchanged baseline. Treat the cold critical path as an **estimate**, not a savings forecast.
3. Run static `history` first. Use opt-in `--replay` when real incremental costs are needed and the repository is trusted; start with a small sample because each event may require a cold parent build.
4. Use saved `history` evidence with `explain`, then `suggest` on relevant packages. Prefer recurring **observed compilation** to simple commit counts or reverse-dependency reach.
5. Use `verify` only when there are existing before/after revisions and controlled experiments are appropriate. It does not implement the suggested refactor.
6. Report the best findings, the evidence for each, what's uncertain, and an actionable next step. Do not modify an investigated repository's source without explicit approval.

```sh
# From the target repository, with gorebuild installed:
gorebuild profile -out build.profile.json.gz ./cmd/app
gorebuild history ./cmd/app --since 90d --replay 10 --out build.history.json.gz
gorebuild explain --history build.history.json.gz --package ./internal/query
gorebuild suggest ./internal/query --history build.history.json.gz
# Only for an explicitly approved existing before/after refactor:
gorebuild verify ./cmd/app --before BASE --after AFTER \
  --package ./internal/query --runs 3 --out build.verify.json.gz
```

`profile` accepts flags **before** the target; `history` accepts them on either side. Use `--details` for diagnosis rather than making default reports exhaustive. `--out` produces one complete artifact, optionally gzip-compressed; existing files are not overwritten. Use a new output name for reruns.

## Evidence and interpretation rules

These distinctions are essential; preserve them in implementation, tests, JSON and prose:

- **Observed**: a captured command, build action or cache input. **Resolved**: source/type-checked relationship. **Estimated**: derived from a DAG or other model. **Verified**: a specified controlled check passed. **Inconclusive**: evidence does not establish the effect.
- **Wall time** is elapsed build latency. **Tool work** sums recorded command durations and can exceed wall time under parallelism. Neither can be substituted for the other.
- `NeedBuild` does **not** prove a compiler ran. Executed work requires a recorded `Cmd`; recognized Go compiler commands distinguish packages genuinely recompiled from build/check-cache nodes. Cgo/assembly counts can overlap build actions.
- An estimated critical path is computed from the dependency DAG. Counterfactuals must recompute the full DAG, including competing paths; zeroing an action gives a **fixed-DAG theoretical upper bound**, not predicted wall savings.
- A change to an imported package does not automatically force every transitive consumer to compile. Source-level reachability, co-changes and cache-key differences do not individually establish a sole invalidation cause.
- A historical commit is **one event** even if it changes several packages. Package event cohorts overlap. Do not sum their wall times, equate package-event medians with package compile time, or present Git commits as literal developer build counts.
- Treat missing, corrupt, unmatched, partial or ambiguous evidence as unknown/unusable, **never as a zero-cost build or a proven absence of an effect**.
- For paired benchmarks, report **after minus before** per run and aggregate paired deltas, not the difference of separate medians. A negative delta means faster after. Keep cold and incremental outcomes separate; same-revision controls estimate noise, not remove it.

## Command-specific boundaries

### `profile`

Use an isolated initially empty `GOCACHE`, then reuse it for the unchanged warm run. Shared module downloads are not compile-cache hits. Default presentation emphasizes the cold dependency path and other expensive off-path work. A warm no-op does **not** simulate an edit. Go's action-graph interface is internal/unstable, so keep parsing version-aware and fail explicitly on incompatible data.

### `history`

Static analysis is first-parent Git events plus the **current** import graph. Replays need detached temporary worktrees, separate fresh caches, the same parent/child build configuration, a verified zero-command warm parent, and captured executed actions. Do not silently substitute a different toolchain or turn excluded/failed replays into successful observations. Record how samples were selected and the actual coverage. Relevant module changes, workspace constraints, external cache programs and path-dependent flags need conservative handling. Builds/replays execute repository code and are **not a security sandbox**.

### `explain`

Saved-event and package explanations should work offline without running new builds. Typed consumer inspection is opt-in, limited to selected packages and build configuration; report incomplete type information instead of inventing objects. Cache-hash replay is a **separate, explicitly expensive** action. Hash differences identify changed inputs, not necessarily which one was sufficient to cause recompilation. Preserve raw evidence and avoid treating inferred paths as causes.

### `suggest`

A suggestion must describe an actual boundary change, not merely a move of declarations. Analyze both incoming and outgoing imports, typed declaration closure, method/receiver families, visibility, state, initialization, generated files, build constraints, transitive reachability and cycles. Retained wrappers or alternate import paths can defeat a proposed isolation. Support bounded factory-result inversion only when typed uses and construction inputs justify it. A source-changing target event cannot be counted as a compilation avoided merely by removing an import. Reject unsupported proposals explicitly and do not manufacture stability or savings estimates. `suggest` is read-only and does not rewrite source.

### `verify`

Compare **existing immutable before/after revisions**, not generated refactors. Build/test correctness before benchmarking; do not claim passing tests prove behavioral equivalence. Keep worktrees, compiler caches, output binaries, and probe application isolated. Establish verified zero-command warm baselines; for repeated incremental arms restore **both** the cache and baseline executable from independent byte copies, not hard links. Distinguish implementation-only, exported-declaration and applicable historical edits. Compare observed package execution sets and cold/incremental timing independently; report noisy, sparse or failing results as such. Private-cache isolation does not prove the global shared cache was unchanged under concurrent host activity.

## Repository changes: expectations

- Read the relevant code and current tests before editing. Prefer a small change scoped to one command or shared calculation, rather than a new general-purpose framework.
- Keep measurement, source analysis, presentation and artifact serialization separate. A `--details` change should not alter analysis, eligibility or raw JSON evidence.
- Preserve the one-file artifact contract; `.json.gz` must decompress to the same JSON payload as `.json`. Never overwrite an existing capture or silently discard raw evidence to make files smaller.
- Validate negative, noisy and unsupported outcomes as carefully as positive ones. Test corrupted graphs, ambiguous hashes, incomplete type checks, cancelled/failed replays and cleanup, in addition to the happy path.
- Avoid running long benchmarks or historical replays as ordinary unit tests. Use synthetic Git repositories for deterministic integration tests; keep large real-artifact validation opt-in.
- Before concluding, run `go test ./...` and `go vet ./...`; use targeted race tests and project checks when relevant. Report what ran and what did not. Do not change the user's Git state, normal Go build cache or output files outside the agreed scope.

## Prometheus lessons to preserve

The real Prometheus work is a **calibration case**, not a target to tune algorithms around:

- Historical analysis covered 477 first-parent events; 20 were selected and 18 replayed successfully. `web/api/v1` compiled in 15/18 successful normal samples, despite only one event directly changing that package. These are overlapping whole-event cohorts, not measured API package latencies. See [history validation](docs/history-validation.md).
- Typed inspection of the `web` → `web/api/v1` relationship resolved 33 referenced exports and 697 reachable declarations with complete checking in the selected configuration. Small outgoing extractions did not remove that dependency. Do not equate a large declaration closure with a viable refactor.
- Incoming-boundary analysis identified a plausible constructor dependency on `storage/remote`, isolated to handler factory calls returning `http.Handler`. A separately implemented refactor moved construction into `web` with lazy providers to preserve important order and gate semantics. This is a specific structural hypothesis, not evidence that arbitrary dependency inversions are safe. See [initial verification](docs/verify-validation.md).
- In expanded verification, API compilation was avoided in **20/20 applicable exported/historical edit pairs**. For the implementation-only edit, API stayed cached in **both** revisions, so there was no avoided compilation. Ten-pair incremental timing for each scenario was **inconclusive** against same-revision controls; cold performance also remained inconclusive with a possible regression. Do not call this a verified end-to-end speedup. See [expanded verification](docs/verify-expanded-validation.md).

When reporting a new result, lead with whether the measured or structural hypothesis held, then the best evidence, important limitations, and the next useful investigation. A credible **no improvement** conclusion is preferable to an overstated optimization claim.
