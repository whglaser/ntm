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

## Enabling reports

pi only writes OSC 7501 after it believes the terminal supports it: it sends
the query and waits for a reply. tmux does not answer for the pane, so inside
tmux the pane must run with:

```
PI_PROGRAM_STATUS=1
```

Because panes inherit the tmux server's environment, the most durable way to
set it is from the shell/profile that starts tmux (for example, add
`export PI_PROGRAM_STATUS=1` to your shell profile). Do **not** put it in a
plugin's `[agent.env]` unless you also accept that `ntm sessions resume` /
automatic recovery will refuse to relaunch the pane: plugin environment values
are intentionally not persisted in the launch specification. `PI_PROGRAM_STATUS=0`
explicitly disables reports.

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