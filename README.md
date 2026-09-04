# Go AI Team

An open cockpit for Claude Code and friends: run every account you own side by
side, drive a board, schedule and trigger agents, review what they wrote, and
let them drive the app back through MCP.

One binary. No Electron, no `npm install`, no subscription.

```
go-ai-team.exe
  ├─ HTTP server  :7777      → web UI, same on desktop and phone
  ├─ WebSocket    /ws/pty    → live terminals
  ├─ WebSocket    /ws/events → status, tokens, accounts, automation
  ├─ POST         /hooks/:t  → signed webhook deliveries
  ├─ PTY manager             → agents, dev commands, SSH shells
  ├─ scheduler + hooks       → unattended runs
  └─ ~/.goaiteam/            → state, profiles, encrypted vault

go-ai-team.exe mcp --project <id>
  └─ JSON-RPC on stdio       → 16 tools an agent can call back into
```

## Why this exists

Claude Code supports multiple accounts through one documented mechanism: the
`CLAUDE_CONFIG_DIR` environment variable. Point it at a different directory and
you get a different account — its own credentials, sessions, transcripts and
quota.

Doing that by hand is miserable, and the tool that does it for you charges
€7.99/month for the privilege. So this does it declaratively instead: pin an
account to a project, override it on a single agent, and every terminal is bound
correctly without you thinking about it.

**Nothing here proxies the Anthropic API and no credential passes through this
program.** It decides which directory a process starts in, and reads the files
the CLI already wrote to your disk.

**Every "AI feature" runs on the subscription you already pay for.** Commit
messages, agent suggestions, feedback clustering, ticket scoping, model routing
and read-aloud summaries all shell out to your own signed-in CLI in headless
mode. There is no metered allowance and no API key to add — which is the whole
point, since the alternative charges per month for a few hundred of them.

## What it does

### Accounts
| | |
|---|---|
| **Unlimited accounts** | An account is a config directory. Add as many as you like. |
| **In-app sign-in** | Opens a real terminal bound to the new profile; you type `/login`, OAuth happens in your browser, and the badge flips the instant credentials land on disk. |
| **Attach what you have** | Point at `~/.claude`, a CCS profile, or any directory with credentials — reused as-is, no re-login. |
| **Discovery** | Scans for provider defaults, CCS instances and `~/.claude-*` siblings. |
| **The cascade** | `agent override → project pin → nearest folder that pins one → global default → ~/.claude`. Unit-tested in every direction, including folder cycles and dangling references. |
| **Verified binding** | Once the CLI writes its own session file, the badge shows the account it *actually* used, not the one we intended. |
| **Honest sign-in state** | Distinguishes signed in from signed out from never signed in — see below, because the obvious check is wrong. |
| **Auto-switch** | At a usage limit: bench the account, copy the transcript into another one, resume the same session there, tell the agent to continue. |
| **Shared user layer** | Links your own commands, skills, subagents, `CLAUDE.md`, hooks and plugins into every account. Credentials stay isolated. |

### Running agents
| | |
|---|---|
| **Agent grid** | Role colours, live status dots, account dot on every avatar. |
| **Split view** | N-way tiling, columns or rows, pinned panes, layout saved per project. Every pane is interactive. |
| **Live terminals** | Real PTYs over websocket into xterm.js, 256KB scrollback replayed on attach. |
| **Morph** | Change a running agent's role in place, keeping its conversation. Optional "fresh eyes". |
| **Fork** | A twin that resumes the parent's actual session, not a summary of it. |
| **Agent messaging** | Saved agents have an inbox. Messages persist to disk before delivery is attempted. |
| **Dev terminals** | Saved per-project commands with live output, reachable from your phone. |
| **SSH** | Saved hosts, one-click shell using your own ssh client, tunnels for private databases. |
| **Restore on launch** | Reopens the agents and commands that were running when you quit. |

### Work intake
| | |
|---|---|
| **Board** | Kanban with drag-and-drop. Dropping a task in *In progress* starts an agent on it. |
| **Idea Radar** | Raw feedback in; themes out, deduplicated, rated on impact and effort, promotable to a ticket. |
| **Ticket scoping** | A PM pass reads the real codebase and writes a brief onto the ticket before any code is written. |
| **Prompt library** | Folders, personal flag, and `{{prompt:name}}` chaining so shared rules live in one place. |
| **Skills library** | `SKILL.md` with triggers, exportable to any runtime that reads the format. |
| **Project memory** | What agents learned — decisions, pitfalls, conventions — surviving the session, read by every agent. |

### Automation
| | |
|---|---|
| **Scheduled tasks** | Every N minutes, hourly, daily, weekly, monthly. No cron expression. A window missed while the app was closed is caught up, not skipped. |
| **Webhook triggers** | A URL and a signing secret per trigger. Signature verified, filter evaluated, burst limit enforced, payload flattened into prompt variables. A delivery that cannot run is queued and replayed. |
| **MCP bridge** | 16 tools exposing the app to the agents inside it: backlog, memory, prompts, commands, messaging, secrets, databases. |

### Review and cost
| | |
|---|---|
| **Per-agent diff** | Filter the working tree by which agent touched which file, read back from each agent's own transcript. |
| **Commit messages** | Written from the real staged diff, in your house style. |
| **Commit context** | Attaches the agent conversation behind a commit as a file in the repo, credentials stripped. Nothing is uploaded. |
| **Token meter** | Input, output, cache writes and reads, cache hit rate, tool uses, models routed — straight from Claude's own JSONL. |
| **Contextual tips** | Chosen from your live numbers, each with a ready-to-send prompt. |
| **Statistics** | Tokens per agent and per account, cache rates, minutes, switches. |
| **Adaptive model** | Reads the prompt before you send it and suggests the cheapest model that will do the job. |
| **Process guard** | Finds child processes that are large, old AND idle at once. Nothing is ended unless you ask. |

### Environment
| | |
|---|---|
| **Secret vault** | DPAPI on Windows, AES-GCM under an owner-only key elsewhere. Referenced as `{{secret:NAME}}` and resolved at launch. There is no code path that returns a value — not to the UI, not to the API, not to an agent. |
| **Databases** | MySQL and PostgreSQL, read-only by default, one statement per call, stacked statements refused, results capped, optional SSH tunnel. Agents query by naming a connection. |
| **Voice** | Dictation and read-aloud on the browser's own Web Speech API — free, no backend, no metered hours. |
| **Doctor** | Answers "why would an agent not start" before you have to guess. |

## Install

Needs Go 1.22+ to build, and Claude Code on your `PATH` to run.

```bash
git clone <this repo> && cd go-ai-team
go build -o go-ai-team.exe .     # or: go build -o go-ai-team .
./go-ai-team.exe
```

It opens in **its own Chrome profile**, as an app window — no address bar, no
bookmark bar, its own taskbar entry, and none of your normal extensions,
cookies or history. See below.

### Its own browser profile

The whole bet of this project is that a browser is a better shell than Electron.
That only holds if the window behaves like an application, so on start it opens
a dedicated Chrome profile living at `~/.goaiteam/browser`:

```
--browser app       own window, own profile   (default)
--browser tab       ordinary tab, own profile
--browser system    your normal browser and profile
--browser none      open nothing
--browser-profile   put the profile somewhere else
```

Chrome, Edge, Brave, Vivaldi and Chromium are all driven the same way; whichever
is found first is used, and if none is present it falls back to your default
browser and says so.

The profile is a real, separate Chrome profile — you can sign into a different
Google account in it, install different extensions, and none of it touches your
browsing. Deleting the folder is safe; it is recreated on the next launch. A
`README.txt` inside says the same thing, because an unexplained 100 MB directory
in a dotfolder is exactly what gets deleted in confusion later.

Preferences are seeded before Chrome starts. The one that earns its keep is
`exit_type: Normal`: without it, Chrome shows the "didn't shut down correctly /
Restore pages?" bubble every single time, because quitting the server closes the
window in a way Chrome reads as a crash.

For a launcher you can double-click, opt in explicitly:

```bash
./go-ai-team.exe --install-shortcut
```

That writes `Go AI Team.lnk` to your desktop (a `.command` on macOS, a
`.desktop` entry on Linux). It is behind a flag rather than automatic, because
writing to somebody's desktop uninvited is not something a tool should decide.

### From your phone

```bash
./go-ai-team.exe --host 0.0.0.0
```

The banner prints a `http://<your-ip>:7777/?token=…` link. Binding beyond
loopback generates a fresh token on every start, because a terminal reachable on
your network without one would be an open shell. Link-local addresses are
filtered out, so the first line is the one worth trying.

### Wiring the MCP bridge into a project

Add this to the project's `.mcp.json`, and its agents gain the 16 tools:

```json
{
  "mcpServers": {
    "go-ai-team": {
      "command": "go-ai-team",
      "args": ["mcp", "--project", "prj_xxxxxxxx", "--agent-name", "Backend Dev"]
    }
  }
}
```

The `--project` scope is the trust boundary: an agent can only reach that
project's backlog, memory and connections.

## First run

1. **Accounts → Find existing accounts.** If you already use Claude Code, your
   `~/.claude` shows up. Attach it.
2. **Add a second account** and sign in, so auto-switch has somewhere to go.
3. **+ Project** → pick a folder. **+ Agent** → name it.
4. Click the card to start it.

To separate work from personal: make a folder, pin the work account to it, file
your work repositories under it. Every project inside starts on that account,
including ones you add later.

## The sign-in check, and why the obvious one is wrong

Logging out of Claude Code does **not** delete `.credentials.json`. It leaves the
file in place, metadata intact, with the token strings blanked:

```json
{"claudeAiOauth":{
  "accessToken": "",            ← blank
  "refreshToken": "",           ← blank
  "subscriptionType": "max",    ← still there
  "refreshTokenExpiresAt": 1789980000000
}}
```

So "the file exists" proves nothing. The first version of this made exactly that
mistake and cheerfully reported two signed-out directories as `2/2 signed in`;
the failure only showed up as a red *Not logged in* line inside the terminal.

That is worse than cosmetic, because the auto-switch fallback picker uses the
same signal — it would have handed a live conversation to a dead account at
precisely the moment the feature exists to rescue it.

The check now reads the file and reports `active`, `refreshable`, `loggedOut`,
`missing` or `unreadable`. Only the first two count as usable. `expiresAt` is
frequently `0` on a working account, so a zero means "not stated" rather than
"expired in 1970".

## How the account binding works

```
Spawn
  ├─ resolve the cascade once, and freeze the answer on the session
  ├─ strip every provider's account variable from the environment
  ├─ strip the parent Claude session's own markers  (see below)
  ├─ set CLAUDE_CONFIG_DIR=<resolved account dir>
  ├─ set CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1
  └─ start `claude` in the project directory, in a PTY

Then, from disk
  ├─ sessions/<pid>.<hash>.key   appears immediately → confirms the account
  ├─ sessions/<pid>.json          appears later      → gives the session id
  └─ projects/<encoded-cwd>/<sessionId>.jsonl        → the token meter
```

The account is resolved **once**, at spawn, and every later display reads that
frozen answer — so the badge on screen can never disagree with the process.

Transcript lookup tries the encoded-cwd path first, then falls back to globbing
`projects/*/<sessionId>.jsonl`. The session id is authoritative, so if Claude
ever changes its folder-naming rule this degrades to a slower lookup instead of
a wrong answer.

### Environment hygiene

Go AI Team is usually launched from a terminal, and that terminal is sometimes
*itself* a Claude Code session, which exports about ten variables describing
itself:

```
CLAUDECODE                     CLAUDE_CODE_MESSAGING_SOCKET
CLAUDE_CODE_CHILD_SESSION      CLAUDE_CODE_MESSAGING_TOKEN
CLAUDE_CODE_SESSION_ID         CLAUDE_CODE_ENTRYPOINT
CLAUDE_CODE_BRIDGE_SESSION_ID  CLAUDE_CODE_EXECPATH
CLAUDE_PID                     CLAUDE_EFFORT
```

Two matter a lot. `CLAUDE_CODE_MESSAGING_SOCKET`/`_TOKEN` point at the *parent*
session's IPC channel. And `CLAUDE_CODE_CHILD_SESSION` makes the CLI skip
writing a transcript — which silently kills the token meter, since there is then
no JSONL to read. This was observed in a real run: the agent booted with
`⚠ Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker`.

So every spawn starts from a filtered environment. The filter is a precise
deny-list plus two narrow patterns (`*_SESSION_ID`, `*_MESSAGING_*`) rather than
the whole `CLAUDE_CODE_` prefix, because legitimate settings like
`CLAUDE_CODE_MAX_OUTPUT_TOKENS` live under that prefix too.

## Auto-switch

When the CLI says it is out of quota:

1. The account is **benched** — using the provider's own reset time when it
   publishes one — even if there is nowhere to switch to, so the next agent does
   not walk into the same wall.
2. A replacement is chosen: signed in on disk, not benched, not opted out, least
   recently used.
3. The transcript is **copied into the incoming account's tree**. A Claude
   session physically lives inside the account that created it, so without this
   `--resume` would open an empty conversation — exactly the loss the feature
   exists to prevent.
4. The CLI is relaunched with `--resume` and nudged to *continue*, not restart.
5. The bench lifts by itself when the window reopens.

**The detector only fires on a real limit message.** Warnings, percentages and
quota panels are ignored by construction, there is a 90-second cooldown so a
resumed conversation cannot re-trigger on its own replayed history, and the
app's own announcement is excluded from matching. A false positive would spend a
subscription you did not intend to spend, so the test suite asserts the
expensive mistakes stay non-matches.

Any account can be marked **never a backup** — the right answer when you keep a
strict line between an employer's subscription and your own.

## Security posture

- **Secrets are write-only through the API.** There is no endpoint that returns
  a value. `{{secret:NAME}}` is resolved in the main process, at launch, into a
  child's environment. A machine with no encryption provider gets a refusal, not
  a plaintext file.
- **Databases are read-only by default.** A write needs a writable connection
  *and* an explicit confirmation, and is refused outright on a connection
  flagged production. Stacked statements are refused; the classifier strips
  comments first and looks through CTEs.
- **Webhooks are signed.** GitHub sha256/sha1, GitLab token and a generic HMAC
  header. A trigger with a secret refuses an unsigned delivery. Tokens and
  secrets are generated server-side, never accepted from the client.
- **MCP is scoped by project.** An agent cannot read or write another project's
  backlog, memory or connections, even with an exact id.
- **The LAN is gated by a per-run token**; loopback is open, everything else
  needs it.
- **SSH host keys are pinned** on first sight and a change is refused.
- **Commit context is redacted** before being written, and stays in your repo.

## Layout

```
main.go, mcp_cmd.go            flags, wiring, banner, the mcp subcommand
internal/store/                persisted state, the cascade, generic collections
internal/accounts/             profile directories, discovery, shared user layer
internal/claudefs/             reads Claude's own on-disk files
internal/session/              PTY manager, status, limit detector, auto-switch
internal/ai/                   headless helper prompts on your own CLI
internal/catalog/              14 built-in roles, subagent-markdown import
internal/automation/           scheduler and webhook engine
internal/browser/              the app's own Chrome profile and launcher
CLAUDE.md                      conventions for anyone (or anything) working here
internal/gitx/                 git for the review pane
internal/guard/                runaway-process finder
internal/secrets/              the vault (DPAPI / AES-GCM)
internal/dbx/                  read-only database access
internal/sshx/                 saved hosts and tunnels
internal/mcp/                  JSON-RPC server and the 16 tools
internal/server/               HTTP API, websockets, embedded UI
internal/server/web/           the UI (no build step)
```

## Tests

```bash
go test ./...                                        # fast, no browser, no network
go test -tags uitest ./internal/server/ -run TestUI  # UI smoke test
```

The UI test launches **its own headless Chrome** on a throwaway temp profile and
drives it over the DevTools protocol: it walks every tab and panel, checks
nothing crashed, and asserts the layout has not grown wider than the window. It
never touches a profile a person is signed into — not yours, and not the app's
own. It is behind a build tag so the ordinary suite stays fast and needs no
browser installed.

52 tests. Covered: the cwd encoder against real transcript directories, the
cascade in every direction, provider isolation, dangling references, folder
cycles, project cascade-delete, the ring buffer's exact-wrap case, the limit
detector's true and false positives, environment filtering, credential auth
states, schedule arithmetic including short months, webhook signatures and
gating, SQL write/stacked-statement classification, vault round-trips including
"the value is not readable on disk", the MCP protocol and its project scope,
JSON salvage, the list-endpoint contract, and the browser profile's preference seeding.

Two scripted loops in `scratchpad/` exercise the running server: 102 endpoint
checks including the failure cases, and an end-to-end automation run that fires
a real signed webhook and asserts the interpolated prompt reached the terminal.

## Not built

Cloud agents (needs a cloud provider), a public feedback portal for clients,
Figma capture, a remote-fleet relay, and the non-Claude providers — the account
model is provider-shaped and the env vars are wired, but only Claude is exercised.
Mongo connections are saved and tunnelled but statements are not executed.

## Licence

MIT.
