# Go AI Team

Run every Claude Code account you own side by side, in one window, on one
machine — with a per-project and per-agent binding, a live token meter, and an
automatic hand-off when one account hits its usage limit.

One binary. No Electron, no `npm install`, no subscription.

```
go-ai-team.exe
  ├─ HTTP server  :7777      → web UI (xterm.js), same UI on desktop and phone
  ├─ WebSocket    /ws/pty    → live terminals
  ├─ WebSocket    /ws/events → status, token and account events
  ├─ PTY manager             → spawns `claude` with CLAUDE_CONFIG_DIR bound
  └─ ~/.goaiteam/            → accounts, projects, agents, settings
```

## Why this exists

Claude Code supports multiple accounts through one documented mechanism: the
`CLAUDE_CONFIG_DIR` environment variable. Point it at a different directory and
you get a different account — its own credentials, its own sessions, its own
transcripts, its own quota.

Doing that by hand is miserable. You export a variable, forget which shell has
which account, and the moment you open a second tab you are guessing. Go AI Team
makes the binding declarative instead: pin an account to a project, override it
on a single agent, and every terminal it launches is bound correctly without you
thinking about it.

**Nothing here proxies the Anthropic API and no credential passes through this
program.** It decides which directory a process starts in, and reads the files
the CLI already wrote to your disk.

## What works today

| | |
|---|---|
| **Unlimited accounts** | An account is a config directory. Add as many as you like. |
| **In-app sign-in** | Opens a real terminal bound to the new profile; you type `/login`, OAuth happens in your browser as usual, and the badge flips the instant `.credentials.json` lands on disk. |
| **Attach what you already have** | Point an account at `~/.claude`, at a CCS profile (`~/.ccs/instances/work`), or at any directory with credentials — reused as-is, no re-login. |
| **Find existing accounts** | Scans for provider defaults, CCS instances and `~/.claude-*` siblings, and offers to attach them. |
| **The cascade** | `agent override → project pin → nearest folder that pins one → global default → ~/.claude`. Fully unit-tested, including folder cycles and dangling references. |
| **Two accounts, one project, at once** | Two agents in the same project can run on two different accounts simultaneously. Verified against real signed-in accounts on disk. |
| **Account colours** | A dot on the agent avatar, the terminal header and the project tile, so what is being billed to whom is never a guess. |
| **Verified binding** | The badge is confirmed against disk: once Claude writes its own session file, the UI shows the account it *actually* used, not the one we intended. |
| **Live terminals** | Real PTYs over a websocket into xterm.js, with 256KB of scrollback replayed on attach so reconnecting mid-run is never a blank screen. |
| **Token meter** | Input, output, cache writes, cache reads, cache hit rate, tool uses, models routed, duration — read straight from Claude's own JSONL transcripts. No proxy, no estimation, no network call. |
| **Contextual tips** | Suggestions chosen *from your live numbers* (low cache hit rate, lopsided turn ratio, heavy reads), each with a ready-to-send prompt. |
| **Auto-switch at a usage limit** | Benches the exhausted account, copies the transcript into another signed-in account's tree, resumes the same session there, and tells the agent to continue rather than restart. |
| **Shared user layer** | Links your own slash commands, skills, subagents, `CLAUDE.md`, hooks and plugins into every account, so a fresh account is not empty. Credentials and history stay isolated. |
| **Phone access** | `--host 0.0.0.0` and the same UI works from your phone's browser. No second app. |
| **Doctor** | Answers "why would an agent not start" before you have to guess. |
| **Honest sign-in state** | Distinguishes *signed in* from *signed out* from *never signed in* — see below, because the obvious check is wrong. |

## The sign-in check, and why the obvious one is wrong

Logging out of Claude Code does **not** delete `.credentials.json`. It leaves the
file in place, with its metadata intact — subscription type, organisation id,
token expiry — and the token strings blanked:

```json
{"claudeAiOauth":{
  "accessToken": "",            ← blank
  "refreshToken": "",           ← blank
  "subscriptionType": "max",    ← still there
  "refreshTokenExpiresAt": 1789980000000
}}
```

So "the credentials file exists" proves nothing. Go AI Team's first version made
exactly that mistake and cheerfully reported two signed-out directories as
`2/2 signed in`; the failure only showed up as a red *Not logged in* line inside
the agent's terminal.

That is worse than a cosmetic bug, because the auto-switch fallback picker uses
the same signal. It would have handed a live conversation to a dead account at
precisely the moment the feature exists to rescue it.

The check now reads the file and reports one of:

| State | Meaning |
|---|---|
| `active` | An access token is present. Usable. |
| `refreshable` | Only a refresh token, but still in date — the CLI will mint a new one. Usable. |
| `loggedOut` | File present, tokens blank. **Not** usable; needs a fresh `/login`. |
| `missing` | No credentials file yet. |
| `unreadable` | File could not be parsed. |

`expiresAt` is frequently `0` on a perfectly working account, so a zero is read
as "not stated" rather than as "expired in 1970". Only the two usable states
count towards the account badge and the fallback pool.

## Install

Needs Go 1.22+ to build, and Claude Code on your `PATH` to run.

```bash
git clone <this repo> && cd go-ai-team
go build -o go-ai-team.exe .     # or: go build -o go-ai-team .
./go-ai-team.exe
```

The UI opens at <http://localhost:7777>.

### From your phone

```bash
./go-ai-team.exe --host 0.0.0.0
```

The banner prints a `http://<your-ip>:7777/?token=…` link. Binding beyond
loopback generates a fresh access token on every start, because a terminal
reachable on your network without one would be an open shell.

### Flags

| Flag | Default | |
|---|---|---|
| `--port` | `7777` | Port to listen on. |
| `--host` | `127.0.0.1` | Bind address. `0.0.0.0` for phone access. |
| `--open` | `true` | Open a browser on start. |
| `--version` | | Print version and exit. |

## First run

1. **Accounts → Find existing accounts.** If you already use Claude Code, your
   `~/.claude` shows up as signed in. Attach it.
2. **Add a second account.** Either attach another directory you already have,
   or add a fresh one and sign in from the app.
3. **+ Project** → pick a folder.
4. **+ Agent** → name it, optionally set an account override.
5. Click the card to start it. The dot on its avatar is the account it is
   spending.

To separate work from personal: make a folder, pin the work account to it, and
file your work repositories under it. Every project inside starts on that
account — including ones you add later.

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

### Environment hygiene

Go AI Team is usually launched from a terminal, and that terminal is sometimes
*itself* a Claude Code session. Such a session exports about ten variables
describing itself, and inheriting them is never right:

```
CLAUDECODE                     CLAUDE_CODE_MESSAGING_SOCKET
CLAUDE_CODE_CHILD_SESSION      CLAUDE_CODE_MESSAGING_TOKEN
CLAUDE_CODE_SESSION_ID         CLAUDE_CODE_ENTRYPOINT
CLAUDE_CODE_BRIDGE_SESSION_ID  CLAUDE_CODE_EXECPATH
CLAUDE_PID                     CLAUDE_EFFORT
```

Two of them matter a lot. `CLAUDE_CODE_MESSAGING_SOCKET`/`_TOKEN` point at the
*parent* session's IPC channel. And `CLAUDE_CODE_CHILD_SESSION` makes the CLI
skip writing a transcript — which silently kills the token meter, since there is
then no JSONL file to read. This was observed in a real run: the agent booted
with `⚠ Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker`.

So every spawn starts from a filtered environment. The filter is a precise
deny-list plus two narrow patterns (`*_SESSION_ID`, `*_MESSAGING_*`) rather than
the whole `CLAUDE_CODE_` prefix, because legitimate user settings such as
`CLAUDE_CODE_MAX_OUTPUT_TOKENS` live under that prefix too and must survive.
`internal/session/env_test.go` pins both halves of that behaviour.

The account is resolved **once**, at spawn, and every later display reads that
frozen answer — so the badge on screen can never disagree with the process.

Transcript lookup tries the encoded-cwd path first, then falls back to globbing
`projects/*/<sessionId>.jsonl`. The session id is authoritative, so if Claude
ever changes its folder-naming rule this degrades to a slower lookup instead of
a wrong answer.

## Auto-switch

When the CLI says it is out of quota:

1. The exhausted account is **benched** — using the provider's own reset time
   when it publishes one — even if there is nowhere to switch to, so the next
   agent you launch does not walk into the same wall.
2. A replacement is chosen among your other accounts of that provider: signed
   in on disk, not benched, not opted out, least recently used.
3. The transcript is **copied into the incoming account's tree**. A Claude
   session physically lives inside the account that created it, so without this
   step `--resume` would open an empty conversation — exactly the loss the
   feature exists to prevent. The original account keeps its copy untouched.
4. The CLI is relaunched with `--resume <sessionId>` on the new account and
   nudged to *continue*, not restart.
5. The bench lifts by itself when the window reopens.

**The detector only fires on a real limit message.** Warnings, percentages and
quota panels are ignored by construction, there is a 90-second cooldown so a
resumed conversation cannot re-trigger on its own replayed history, and the
app's own announcement is excluded from matching. A false positive would spend a
subscription you did not intend to spend, so `internal/session/quota_test.go`
asserts the expensive mistakes stay non-matches.

Any account can be marked **never a backup** — the right answer when you keep a
strict line between an employer's subscription and your own.

Needs at least two signed-in accounts of the same provider to do anything.

## Layout

```
main.go                        flags, banner, graceful shutdown
internal/store/                persisted state + the cascade resolver
internal/accounts/             profile directories, discovery, shared user layer
internal/claudefs/             reads Claude's own on-disk files
internal/session/              PTY manager, status heuristic, limit detector, auto-switch
internal/server/               HTTP API, websockets, embedded UI
internal/server/web/           the UI (no build step)
```

## Tests

```bash
go test ./...
```

Covered: the cwd encoder against real transcript directories, the cascade in
every direction, provider isolation, dangling references, folder cycles, the
ring buffer's exact-wrap case, argument splitting, and the limit detector's
true and false positives.

## Not yet built

Multi-provider agents (Codex, Grok and Cursor account plumbing is in place, the
UI is Claude-only), split view, the kanban backlog, scheduled tasks, webhook
triggers, MCP servers exposing the app to agents, and voice. The account model
is provider-shaped already: each one isolates an account behind a single
environment variable, so adding one is wiring, not a redesign.

## Licence

MIT.
