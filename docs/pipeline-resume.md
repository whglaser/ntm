# Pipeline resume playbook: `--start-from` + `--from-state`

Long pipelines fail late. A security-design review that dies in the `answers`
step after intake, mapping, questions, and four evidence lanes has hours of
completed work behind it. Re-running the whole workflow wastes that work and
tokens; re-running from the failed step is only safe when the earlier step
outputs are still available for substitution.

ntm persists step results (outputs, parsed data, delivery evidence) to
`.ntm/pipelines/<run-id>.json` after every step. Two flags turn that persisted
state into a recovery path:

| Flag | Meaning |
| --- | --- |
| `--start-from <step-id>` | Begin execution at this **top-level** step. Every transitive dependency, plus the inline children of skipped parallel/loop containers, is recorded as `Skipped` and not dispatched. |
| `--from-state <run-id>` | For each skipped step, copy the persisted result (output, parsed data, pane/agent metadata) from `<run-id>` so `${steps.<id>.output}`, `${steps.<id>.data}`, and the step's `output_var` still resolve. Requires `--start-from`; supplying it alone is an error. |

The persisted state lives in the **project directory**, so run both commands
from the project root (or pass the same `--session`/project that owns the
workflow).

## Recipe

```bash
# 1. Find the failed run and the last completed step.
ntm pipeline list
ntm pipeline status run-20250101-120000-abcd

# 2. Rerun at the failed step, seeding skipped outputs from the prior run.
ntm pipeline run .ntm/workflows/review.yaml \
  --session myproject \
  --start-from answers \
  --from-state run-20250101-120000-abcd \
  --var review_slug=example

# Background runs take the same flags:
ntm pipeline run .ntm/workflows/review.yaml \
  --session myproject --background \
  --start-from answers --from-state run-20250101-120000-abcd
```

The run log proves the reuse happened:

```text
INFO pipeline.start_from.applied run_id=run-... workflow=... target=answers \
     skipped_count=11 with_prior_state=true
```

`with_prior_state=false` means only `--start-from` was passed. A recovery run
doing exactly that only survived because its prompts re-read files from disk; do
not rely on that. Always pass `--from-state` when a prior run holds the outputs.

## What is reused, and what is not

Reused: the skipped steps' `Output`, `ParsedData`, `PaneUsed`, `AgentType`, and
their output variables (`steps.<id>.output`, `steps.<id>.data`,
`<output_var>`, `<output_var>_parsed`).

Not reused: side effects. Skipped commands and agent turns are **not**
re-dispatched, so files or external state they produced are assumed to still
exist. `--from-state` copies evidence, not results on disk. That is exactly why
the workflow's own verify gates matter.

## Verify gates

Add fail-closed `command:` gates after any step whose artifacts must exist, and
declare the artifacts under `outputs:` so the run reports missing paths:

```yaml
outputs:
  - name: question-answers
    path: reviews/active/${vars.review_slug}/planning/question-answers.md

steps:
  - id: verify_analysis_artifacts
    command: |
      set -eu
      test -s "reviews/active/${vars.review_slug}/planning/question-answers.md"
      printf 'analysis_artifacts_present\n'
    output_parse: first_line
    timeout: 2m
```

A review workflow can carry gates such as `verify_standards_coverage`,
`verify_lane_notes`, `verify_analysis_artifacts`, and `verify_report_artifacts`
for this. On a resumed run they are the difference between "the persisted
output substituted" and "the artifact the next step needs is silently absent".

## `--start-from`/`--from-state` vs `ntm pipeline resume`

| | `ntm pipeline resume <run-id>` | `--start-from` + `--from-state` |
| --- | --- | --- |
| Run identity | Same run ID, continues where it stopped | New run ID |
| Use when | The run was interrupted; delivery receipts and in-flight evidence should be honored | You want a fresh attempt at a chosen step, seeded from an older completed run |
| Agent deliverables | Resumes observing the original pane without resending; a saved response is reused | Skipped steps are never dispatched; the target step dispatches normally |
| Failure handling | Unknown submission outcomes and replaced panes require inspection; `--mode=restart-failed` deliberately repeats them | The target step repeats its side effects; earlier steps do not |

Use `--start-from`/`--from-state` for "redo from step N using the good outputs
before N". Use `ntm pipeline resume` for "continue the run that stopped at N".
They compose: inspect with `ntm pipeline status`, resume the interrupted run if
its own state is trustworthy, otherwise start a new run from the last step whose
outputs are known good.

## Troubleshooting

- `--from-state requires --start-from` — pass both; the skip set is what the
  prior outputs are applied to.
- `${steps.<id>.output}` resolves to an empty string in the resumed step — the
  prior run never persisted an output for `<id>`, or `<id>` is not a transitive
  dependency of the target. Check `ntm pipeline status <prior-run>` and the
  `skipped_count` in the new run's log.
- `--from-state: load run "<id>": ...` — the run ID has no state file in this
  project. Run from the project root that owns `.ntm/pipelines/`.
- Background workers write the same state files and a private worker log under
  `.ntm/pipelines/background/`; use it to confirm `with_prior_state=true` on
  detached runs.