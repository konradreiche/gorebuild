# gorebuild

Find out what makes Go builds slow, which changes cause expensive rebuilds, and whether a refactor actually helps.

`gorebuild` combines Go build-action measurements, Git history, and source analysis. It starts with the build you care about and helps you move from a bottleneck to evidence-backed options for improving it. It does not rewrite your code.

## Get started

Install the latest version:

```sh
go install github.com/konradreiche/gorebuild/cmd/gorebuild@latest
```

Then, from the Go repository you want to investigate, profile a build target:

```sh
gorebuild profile -out build.profile.json.gz ./cmd/app
```

You'll see the cold-build critical path, other expensive actions, and the time taken by an unchanged warm build. The saved file contains the full evidence, not just the terminal summary.


## Agent instructions

Copy this prompt into your coding agent's instructions:

```text
Use `gorebuild` to investigate build performance in this repository. Identify the main Go build target, then use `profile`, `history --since 90d --replay 20`, `explain`, and `suggest` to find actionable optimization opportunities. Use `verify` where a concrete refactoring can be evaluated. Prioritize recurring incremental rebuild costs over isolated slow compilation. Distinguish measured results from estimates, and account for uncertainty and architectural tradeoffs. Do not modify source code without approval. Report the most promising findings, supporting evidence, and recommended next steps. Avoid speculative optimizations.
```

## Investigate incremental rebuilds

Cold builds and everyday edits have different bottlenecks. To see which packages change often, start with Git history:

```sh
gorebuild history ./cmd/app --since 90d
```

To **measure** representative historical rebuilds rather than just estimate their impact, opt into replay:

```sh
gorebuild history ./cmd/app --since 90d --replay 10 \
  --out build.history.json.gz
```

Replay can be expensive because each sampled change needs its own cold parent build. Start with a small sample.

Use the saved history to investigate a particular package or commit:

```sh
gorebuild explain --history build.history.json.gz \
  --package ./internal/query

gorebuild explain --history build.history.json.gz \
  --commit COMMIT_HASH
```

Then look for possible dependency-boundary improvements:

```sh
gorebuild suggest ./internal/query \
  --history build.history.json.gz
```

Suggestions are hypotheses, not automatic edits or guaranteed speedups. Some will be rejected because they would not change the relevant dependency graph.

## Verify a refactor

If you've implemented a proposed change in a separate revision, compare it with the baseline:

```sh
gorebuild verify ./cmd/app \
  --before BASELINE_COMMIT --after REFACTORED_COMMIT \
  --package ./internal/query --runs 3 \
  --out verification.json.gz
```

`verify` checks correctness, compares dependency graphs, and measures controlled cold and incremental builds. It distinguishes avoided compilation from observed wall-clock improvement, which may remain inconclusive.

## Reading the results

| Result | What it means |
| --- | --- |
| **Wall time** | Time you actually waited for the build. |
| **Tool work** | Sum of recorded tool durations; parallel work can exceed wall time. |
| **Critical path** | An estimated longest dependency chain, not a guaranteed saving. |
| **Observed / resolved / estimated** | Recorded execution, typed dependencies, or model-derived findings. |
| **Verified / inconclusive** | A checked structural outcome, or insufficient evidence for a performance claim. |

Reports lead with the finding and its limitations. Add `--details` for supporting paths and diagnostics. Use `--out report.json.gz` to save complete, compressed evidence in one file. Existing output files are not overwritten.

## Safety and limitations

Builds and replays run code and tools from the repository. **Only replay or verify revisions you trust.** `gorebuild` uses isolated worktrees and build caches for measurements rather than replacing your normal `GOCACHE`; module downloads may still be shared.

A slow package is not necessarily worth refactoring. A frequently changed package may not invalidate its consumers, and eliminating compiler work may not reduce wall time. `gorebuild` keeps those distinctions visible.

## More information

- [Detailed command reference](docs/reference.md): flags, replay behavior, source inspection, constraints, and evidence semantics.
- [Reporting examples](docs/reporting.md)
- [Historical replay validation](docs/history-validation.md)
- [Refactoring verification](docs/verify-validation.md) and [expanded experiments](docs/verify-expanded-validation.md)
