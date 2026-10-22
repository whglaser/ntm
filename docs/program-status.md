# Program Status (OSC 7501)

NTM can use an agent's **own** state reports instead of guessing state by
scraping TUI chrome. pi (pi-tui) emits the terminal *Program Status Protocol*
as OSC 7501:

```
ESC ] 7501 ; state=<state>[:app=<app>][:kind=<kind>][:msg=<base64>] ST
```

`ST` is `ESC \`; BEL (`0x07`) is accepted as well. The body is a
colon-separated list of `key=value` pairs; keys NTM understands:

| Key | Required | Meaning |
|-----|----------|---------|
| `state` | yes | `working`, `blocked`, `done`, `error`, `idle`, or `clear` |
| `app` | no | application name (pi reports `pi`) |
| `kind` | no | dialog kind, present only while `state=blocked` |
| `msg` | no | base64 (std) UTF-8 text; pi limits it to the session name, dialog titles, and the first line of errors — prompts and assistant output are never included |

There is also a capability query, whose body is just `?`, and a `clear` report
that pi writes on shutdown to remove a previously reported status.

State semantics (pi 1.x):

| `state` | Meaning | NTM mapping |
|---------|---------|-------------|
| `working` | agent run or compaction in flight | `working` |
| `blocked` | a dialog waits for the user | `working` (never `idle`: the pane is not available for new work) |
| `done` | last run settled successfully | `idle` |
| `idle` | run aborted / session reset | `idle` |
| `error` | last assistant message ended with an error | `error` |
| `clear` | status withdrawn | stored report removed; source falls back to scraping |

## How NTM captures it

OSC sequences never survive into a rendered pane capture: the terminal (tmux)
consumes them. NTM therefore taps the pane's **raw output stream** with
`tmux pipe-pane` (the same `tmux.PaneStreamer` machinery used by `ntm serve`)
and parses reports out of the byte stream incrementally — a sequence split
across reads or write chunks is reassembled. Polling fallback captures are
never used as a status source, so a scrape can never masquerade as an
authoritative report.

The detector reports which source decided a state: statuses carry
`state_source: "osc"` when the agent's own report won, `"scrape"` when the
content analysis had to. A fresh report is preferred; a report older than 30
minutes (or one removed by `clear`) falls back to scraping.

Today this is wired into:

- **pipeline runs** (`ntm pipeline run` / background pipelines / stage
  pipelines): the executor attaches a tap to each pane before it pastes a
  prompt, and the completion wait prefers the agent's reports. This is the fix
  for idle pi panes being misread as working and burning the step timeout.
- **`ntm serve`**: streamed panes are decoded and published as `pane.status`
  WebSocket events on the pane's stream topic, and the latest report per pane
  is available at `GET /api/v1/streaming/program-status` (optional
  `?pane=<target>` filter).

`ntm status`, `ntm wait`, and `ntm activity` still classify by scraping; the
source-tagged detector is in place for them to adopt.

## Completion robustness (hysteresis + artifact fallback)

Pipeline completion waits are deliberately paranoid, because status detection
is a screen-scrape heuristic (or, for pi, a change-driven report that a lost
pipe can silence):

- **Hysteresis**: the detector must report idle for a configurable number of
  consecutive polls (`ExecutorConfig.IdleStablePolls`, default 3) before a
  step completes. Any non-idle reading resets the streak, so a single
transient idle frame cannot end a step early.
- **Per-step reset**: when a pane lock is acquired for a new step, the
  executor drops that pane's cached classification history (content
  fingerprint and the OSC report left by the previous lifetime). A state
  derived from the previous step cannot persist into the next one.
- **Artifact stability**: a step may declare the files it is expected to
  produce:

  ```yaml
  - id: answers
    pane: "${vars.pane_reasoner}"
    template: answers.md
    timeout: 3h
    outputs:
      - planning/question-answers.md
      - {name: graph, path: planning/question-graph.md}
  ```

  Once every declared path exists and its bytes have been identical for the
  stability window (`ExecutorConfig.OutputStabilityWindow`, default 15s), the
  step completes even if the pane still reads working. This is the
  answers-style long step: the deliverable is on disk, and a stuck
  classification must not burn the rest of the timeout. The check is
  independent of pane state and of the output-progress gate; a path that
  never appears can never complete the step, and deleting or rewriting an
  output resets the window. Declared paths are variable-substituted exactly
  like `Workflow.outputs`.

## Enabling reports

pi only writes OSC 7501 after it believes the terminal supports it: it sends
the query and waits for a reply. tmux does not answer for the pane, so inside
tmux the pane must run with:

```
PI_PROGRAM_STATUS=1
```

ntm-spawned pi panes get that automatically: a pi plugin's spawn command is
prefixed with `PI_PROGRAM_STATUS=1`, so the assignment is part of the
persisted launch command and `ntm sessions resume` / automatic recovery
replay it without a manual export. An explicit `PI_PROGRAM_STATUS=0` in the
spawning environment is the off-switch.

For a pi pane started outside ntm, export it from the shell/profile that
starts tmux. Do **not** put it in a plugin's `[agent.env]`: plugin environment
values are intentionally not persisted in the launch specification, and a
spec that omits them is refused on replay.

## Troubleshooting

- **Sources show `scrape` on pi panes**: the pane was launched without
  `PI_PROGRAM_STATUS=1` (or with `=0`), or the tap could not attach
  (`pipe-pane` fails when no tmux server is reachable).
- **No `pane.status` events in `ntm serve`**: start the pane stream first
  (`POST /api/v1/sessions/{session}/panes/{idx}/stream`); reports are only
  decoded from a live tap, not from one-shot captures.
- **Raw inspection**: `tmux pipe-pane -t <pane> 'cat >> /tmp/pane.log'`, run a
  turn, then search the log for `]7501;`. Byte fixtures from real captures live
  in `internal/programstatus/parser_test.go`.
- **Another consumer owns the pane's pipe**: tmux allows one `pipe-pane`
  command per pane. A pipeline tap and an `ntm serve` tap for the same pane
  compete; the loser falls back to polling and stops producing OSC events.
  Stop the other consumer (or its stream) before relying on OSC status.