# Conclusion-first CLI reports

These changes are presentation-only. Measurement engines, replay/verification
checks, typed-source analysis, candidate eligibility and statistical rules are
unchanged. Existing artifact fields retain their meanings and raw evidence is
not removed. `--details` affects terminal rendering, not analysis configuration.

## Progressive detail

- **Default:** the command's finding, key measurements, limitations and next step.
- **`--details`:** supporting action/path/distribution evidence, diagnostics,
  constraints and individual suggestion rejection reasons. Existing detailed
  report functions remain available; the new `Render` functions add summaries.
- **`--out FILE.json.gz`:** the same complete existing evidence payload compressed
  into one file. Plain `.json` is also supported. History/verify already supported
  compression; profile/explain/suggest now do too. Gzip streams are closed and
  checksummed before committing the output; write failures remove partial files
  according to each command's existing artifact policy. Existing files are never
  overwritten.

Reports use 80 columns by default or the exported `COLUMNS` setting. Tests exercise
80 and 120 columns, including long paths. Tables wrap rather than discard names.
Values use seconds to three decimal places. Small values can therefore round to
`0.000s`; original nanoseconds remain in the evidence artifact. Paired wall
 differences use an explicit sign: after minus before, negative means faster after.

## Verify: frozen Prometheus reference

The expanded evidence from `verify-expanded-validation.md` renders as:

```text
gorebuild verify: storage/remote

Structural result:
  Check                           Result
  Dependency removed              Verified
  web/api/v1 compilation avoided  20/20 applicable runs
  Cached in both revisions is not recompilation avoided.

Incremental performance:
  Scenario                 Wall delta  Conclusion
  Implementation-only      +0.393s     Inconclusive
  Exported declaration     -0.963s     Inconclusive
  Inverse historical edit  -2.061s     Inconclusive
  Paired after-minus-before median; negative means after was faster.

Cold performance:
  Measurement                    Result
  Median paired wall difference  +12.104s
  Conclusion                     Inconclusive

Correctness:
  Check                  Result
  Builds                 Passed
  Tests and race checks  Passed
  Runtime smoke checks   Passed

Assessment:
  Cache isolation verified for the applicable measured edits.
  End-to-end performance improvement not established.
  Do not adopt solely for build speed.
  Tests do not prove equivalence; other configurations remain untested.
```

The 20 applicable runs are the ten exported-edit and ten inverse-historical-edit
pairs. The implementation-only scenario is **not** counted: API stayed cached
before and after. Mixed compiler counts are not converted into universal avoided
compilation claims. Invalid performance is withheld; missing medians/checks are
explicitly unavailable/not checked. Detailed verification progress is sent to
stderr only when `--details` is requested; the existing expensive-operation
warning remains visible by default.

## Other commands

- **Suggest:** supported incoming boundaries first, with human-readable proposed
  construction moves, removed/added edges, historical counts, feasibility,
  outstanding constraints and conditional/unverified benefit classification.
  Rejected outgoing groups and incoming boundary proposals are counted separately
  from their actual statuses, never invented categories or savings. The frozen
  Prometheus artifact has **12 outgoing groups and 125 incoming proposals** rejected;
  individual reasons are in `--details` and all evidence remains in `--out`.
- **Explain:** observed execution and expensive actions, inferred/estimated paths,
  observed cache inputs, resolved typed-source dependencies and unverified
  architectural possibilities remain distinct. Reachability is not invalidation
  causality. Selected-package cache blocks take display priority over target/main
  blocks; detailed input entries retain their full bytes. Default output does not
  dump declaration graphs. The existing selected-explanation JSON is preserved.
- **History:** period/completeness and analyzed/selected/successful event counts,
  changed-package frequency, observed compiler recurrence and whole-event wall
  durations. Package build-node tool-work medians are separately labeled. Cohorts
  overlap; neither frequency nor estimated reach proves a miss. Default output
  omits opaque impact/Pareto prioritization. The saved Prometheus evidence still
  shows **477 analyzed / 20 selected / 18 successful**, API compilation **15/18**,
  whole-event median **12.581s** and two explicit module-configuration exclusions.
- **Profile:** cold critical path first, action duration shares against the full
  estimated path and a short off-path competing-work list. Shares are not achievable
  wall improvements; parallel work can exceed wall time. `--dependency-order`
  remains supported. Warm unchanged builds do not measure source-edit cost.

## Validation and separation

`internal/presentation` contains only small duration/path/table/wrapping helpers;
`internal/artifact` supplies the explain/suggest JSON encoder with optional gzip.
Per-command renderers consume existing structured records; no generic reporting
framework or measurement options are introduced.

Tests cover both widths, numeric units/signs, sparse/failure states, structural
success with inconclusive performance, edited correctness failures, rejected
candidate summaries, partial/complete history, critical-path shares/order,
`--details`, immutable input records and byte-identical decompressed JSON.
Existing history/verify artifact tests continue checking their compressed output.

Real-artifact reference validation is opt-in and completely offline:

```sh
GOREBUILD_REPORTING_PROMETHEUS=/path/to/prometheus \
  go test ./internal/verify -run TestOptionalPrometheusTerminalReports -v -count=1
```

It reads existing verification, suggestion and history artifacts, skips heavy raw
captures when only summaries are needed, and renders at both widths without
executing target builds, tests, Git commands or cache replay. The local validation
log is `/var/tmp/gorebuild-reporting-validation.txt`. No Prometheus measurements
were repeated or evidence artifacts modified for this presentation work.
