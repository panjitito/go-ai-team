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
| **Drag to organise** | Drag a project onto a folder to file it, or onto the drop zone to take it back out; folders nest the same way. A folder cannot be dropped inside itself or its own descendants — that would leave the subtree alive, still pinned to accounts, and unreachable — and the server refuses it too, not just the sidebar. |
| **Verified binding** | Once the CLI writes its own session file, the badge shows the account it *actually* used, not the one we intended. |
| **Honest sign-in state** | Distinguishes signed in from signed out from never signed in — see below, because the obvious check is wrong. |
| **Auto-switch** | At a usage limit: bench the account, copy the transcript into another one, resume the same session there, tell the agent to continue. |
| **Shared user layer** | Links your own commands, skills, subagents, `CLAUDE.md`, hooks and plugins into every account. Credentials stay isolated. |

### Running agents
| | |
|---|---|
| **Agent grid** | Role colours, live status dots, account dot on every avatar. |
| **Conversation view** | Rendered from the transcript the CLI writes, not scraped off the terminal: real turns, markdown, tool calls as one-line cards. It says so while the agent is working — what it is doing, and for how long. The terminal is a toggle away. |
| **Paste an image** | Paste or drop a screenshot into the composer, click the thumbnail to check it full size, and read it back as a picture rather than as a file path. It is written beside the agent and its path goes into the prompt, so this works from your phone too — the CLI can only read the clipboard of the machine it runs on. |
| **Answer its questions** | "Do you want to create hello.txt?" is the most frequent thing Claude Code says, and it draws it on the terminal rather than writing it to the transcript. The box is read off the terminal and offered as buttons, marked with the option the CLI's own cursor is on. Clicking one writes that digit into the pty, which is exactly what pressing the key does. Also covers the first-run boxes — theme, login method, folder trust — which the conversation used to show as nothing at all. |
| **Answer its questions, part two** | The picker `AskUserQuestion` draws — the agent wanting a decision before it carries on — is answerable here too, including the second question and the confirm step. It shows numbers but ignores them, so clicking moves the CLI's own highlight and presses Enter, checked against the screen before the Enter goes out. |
| **Interrupt** | Escape, as the CLI's own footer suggests: stops the turn and keeps the session. Stop still ends it. Having only Stop meant an agent heading in the wrong direction cost you the whole conversation. |
| **Permission mode** | auto, manual, accept edits, plan — switched from the bar above the composer and read back from the CLI, so what is shown is what the session is in. There is no command that jumps to a mode, so the app cycles shift+tab until the terminal agrees; if a mode is not on offer for that model, it says so instead of pretending. |
| **Says when it needs you** | "waiting" means two different things — waiting for the model, and waiting for a person — and only one is worth walking back to the desk for. The one with a question on screen is marked on its card, counted in the window caption, and flashes the taskbar button when it stops to ask. A flash, not a focus grab: an agent's question is not a reason to yank the cursor out of whatever you are typing elsewhere. |
| **Rewind** | One click puts the CLI's own rewind picker on screen — restore the code and the conversation to an earlier point. A hand-off rather than a reimplementation, and deliberately: it is an arrow-key list rather than a numbered box, so it cannot become buttons the way a permission prompt can, and picking the wrong row loses work. What did improve is reading it: prompts drawn by the CLI are now rendered on a real character grid instead of having their escape sequences stripped, so the box in the banner is the box on screen rather than a wall of spinner frames. |
| **`@` completes a path** | Type `@` and a few letters of a filename to pick it from the project — git's file list where there is a repository, so it is your files and not your node_modules. The CLI expands `@path` into the real file when the message is sent, checked by asking an agent to quote a file it had not opened. A path with a typo in it is worse than none: the agent goes looking and reports that it does not exist. |
| **The agent's plan** | Rebuilt from its own TaskCreate/TaskUpdate calls and shown above the composer as "3/7 · Writing the database schema", with the whole list a click away. The CLI shows this as it works; the app showed nothing, which across a row of agents is the difference between knowing what each one is doing and guessing. |
| **What it changed, beside the chat** | A rail down the right listing the files this agent has written to, newest first, with a mark for the ones it created and a count for the ones it kept coming back to. Clicking one shows its diff without leaving the conversation. Read from the agent's own tool calls rather than from git — the two answer different questions, and a file it edited and then reverted belongs on this list and not in git's. |
| **Files** | Browse the project, read a file with line numbers and highlighting, edit it and save. A save carries the hash the file was opened at, so if an agent rewrote it meanwhile the save is refused rather than clobbering their work. |
| **A worktree each** | Several agents on one repository edit the same files, and one's half-finished change silently becomes another's starting point. Switch it on per agent and each gets its own git checkout on its own branch off the same history — working in parallel, merged deliberately. The trees live in the app's state directory, not inside the repo, so they never appear in the file browser or in `git status`. Review → Worktrees lists them; removing one asks git first, and git refuses to throw away uncommitted work. |
| **Activity in the tree** | A project's badge says what its agents are doing, not just how many there are: working pulses, waiting-on-you is steady and bright, running-but-idle is a quiet dot, and the moment the last one stops working it flashes green once and settles. That last one is a transition, so it is the thing nothing could report before — you had to be watching. Collapsed, the ☰ carries the same summary. |
| **Collapse the panel** | The projects panel is 268px of a window that is mostly conversation, and you only need it while switching project. ☰ or Ctrl-B hides it and gives the space to the work; the choice is remembered per browser, because a phone and a desktop want different answers. On a phone the same button opens it as an overlay, which is what it already did. |
| **Go to anything** | Ctrl-K lists every agent, project, panel and view and filters as you type. Agents first and carrying their state, because "which one wanted me" is the question being asked most of the time: one waiting on an answer sorts above one that is working, which sorts above one that is idle. Matching is a subsequence, so `bkw` finds *Backend worker* without anyone having to remember the words. The topbar carries the button with its key printed beside it — a shortcut nobody can see is a shortcut nobody uses. |
| **Desktop alerts** | The point of running eight agents is that you are not watching any of them, and the only way to learn that one had stopped to ask something was to come back and look. A notification and a short chime when an agent asks, when a session dies, and — if you ask for it — when a turn finishes. Off until switched on in Settings, because a permission prompt nobody invited is its own kind of rude, and silent while the window is in front of you. A wave arrives as one line: sending the same prompt to six agents ends six turns at once, and six notifications up the side of the screen get dismissed unread. |
| **The keyboard, written down** | `?` opens the list of shortcuts — but only when it is a question and not a character being typed. A test reads the handlers and fails if the list has stopped matching them. |
| **Split view** | N-way tiling, columns or rows, pinned panes, layout saved per project. Every pane is interactive. |
| **Live terminals** | Real PTYs over websocket into xterm.js, 256KB scrollback replayed on attach. |
| **Morph** | Change a running agent's role in place, keeping its conversation. Optional "fresh eyes". |
| **Fork** | A twin that resumes the parent's actual session, not a summary of it. |
| **Agent messaging** | Saved agents have an inbox. Messages persist to disk before delivery is attempted. |
| **Dev terminals** | Saved per-project commands with live output, reachable from your phone. |
| **SSH** | Saved hosts, one-click shell using your own ssh client, tunnels for private databases. |
| **Restore on launch** | Reopens the agents and commands that were running when you quit. |
| **A window of its own** | An agent runs for half an hour and you want to watch it while working in another, which on a desk with two monitors means two windows and not two tabs in one. ⧉ on a conversation opens that agent in its own window; ⧉ on a project opens the project. A pop-out is the same page with a starting position in its address, so there is no second UI to keep in step — it opens, reads what it is for out of its own URL, and goes there, with the same event socket and the same answers-to-questions as any other window. A window onto one conversation drops the project list and the tabs, because every pixel of frame is a pixel not showing the conversation; a window onto a project keeps its tabs, because moving between the board and the files is the whole point of it. Asking twice raises the window you already have. From a phone it opens a tab instead, which is the honest thing a phone can do. |
| **Quit, from inside** | Ctrl-C in a console was the only way out, and the console is given back at startup now so that no black box sits behind the window. Quit lives in Settings and in Ctrl-K, asks first and says how many sessions it is about to end. It is also what lets every launch mode drop its console, not just the desktop window — a browser tab has no window to close and no icon in the notification area. `--browser none` keeps its terminal, because somebody running this as a bare server has nothing else to stop it with. |
| **Closing the window does not stop the work** | The X drops the app to the notification area and the agents carry on, because the window is a view onto a server that is perfectly happy without it. Click the icon to come back; Quit is on its right-click menu. A hidden window has no taskbar button to flash, so while it is down there an agent's question raises a balloon from the icon and the icon's tooltip says how many are waiting — that works whether or not the browser notifications were ever switched on. The first time it hides it says so, once, because somebody who closes a window expects it to be closed. |

### Work intake
| | |
|---|---|
| **Board** | Kanban with drag-and-drop. Dropping a task in *In progress* starts an agent on it. |
| **Idea Radar** | Raw feedback in; themes out, deduplicated, rated on impact and effort, promotable to a ticket. |
| **Ticket scoping** | A PM pass reads the real codebase and writes a brief onto the ticket before any code is written. |
| **Prompt library** | Folders, personal flag, and `{{prompt:name}}` chaining so shared rules live in one place. |
| **Skills library** | `SKILL.md` with triggers, exportable to any runtime that reads the format. |
| **MCP servers, per account** | Every account's servers in one list, and a copy between them. Sharing your user layer does not cover these: they live in `.claude.json`, which also holds session history and per-project state, so it cannot be linked the way commands and skills are — and a newly signed-in account therefore has none of them, with nothing to say so. Values are never shown; the copy moves them file to file so a database password does not pass through the API or a command line. |
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
| **Split diff** | Side by side with line numbers on both sides, git's own hunk context, and the changed fragment marked *inside* an edited line — so a renamed variable does not look like a rewritten one. Unified is a toggle away, and a new file shows its contents instead of "no textual diff". |
| **Commit messages** | Written from the real staged diff, in your house style. |
| **Commit context** | Attaches the agent conversation behind a commit as a file in the repo, credentials stripped. Nothing is uploaded. |
| **Token meter** | Input, output, cache writes and reads, cache hit rate, tool uses, models routed — straight from Claude's own JSONL. |
| **Contextual tips** | Chosen from your live numbers, each with a ready-to-send prompt. |
| **Statistics** | Tokens per agent and per account, cache rates, minutes, switches. |
| **Adaptive model** | Reads the prompt before you send it and suggests the cheapest model that will do the job. |
| **Process guard** | Finds child processes that are large, old AND idle at once. Nothing is ended unless you ask. |
| **Search every conversation** | Ctrl-Shift-F over everything the agents have ever said — every message, every tool call, every result. Before this, an answer you did not remember the location of was gone. 2.3GB across 1,127 transcripts here, read in 2.7 seconds by four readers at once, because the scan is a raw substring match over the bytes and JSON is parsed only for the lines that already matched. It searches the subagents too, which are 858 of those files and where most of the detail is. The footer says how much was actually read: "nothing found" and "nothing found in the part I had time for" are different answers. |
| **What happened while you were out** | An Activity tab: started, asked, finished, handed over at a quota limit, died — with the time, the agent and the project. Kept by the server rather than the page, because the hours worth reading about are the ones when nobody had it open. Token counters are not in it; every busy agent emits one every few seconds and they would be the only thing there. |

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

It opens in **a real application window**: its own icon in the taskbar and
Alt-Tab, native title bar and controls, no address bar, no tabs, and no browser
to install or borrow. See below.

### A native window, not Electron and not Chrome

The UI is a web page, which is what lets the same app open on your phone and
costs nothing to ship. What it should never have needed is *somebody else's
browser* to display it — that meant a Chrome dependency, a profile directory to
manage, and a window that was still recognisably a browser pretending not to be
one.

Windows already ships an embedded web view: **WebView2**, part of Edge, present
on every Windows 11 machine. The app uses it directly. The binding is pure Go,
so this is still one no-cgo binary; it grew by about a megabyte, not by the
hundred that bundling a browser engine costs.

```
--browser desktop   a real app window, no browser   (default)
--browser app       chromeless window in the app's own Chrome profile
--browser tab       ordinary tab, own profile
--browser system    your normal browser and profile
--browser none      open nothing
--browser-profile   put the Chrome profile somewhere else
```

The window remembers where you left it — position, size, and whether it was
maximized, keeping the restored size separately so maximising and quitting does
not lose it. Closing it shuts the app down properly: what was running is
recorded first, so **Restore on launch** brings it back.

If the WebView2 runtime is missing, it falls back to `app` rather than failing —
you get the UI in Chrome instead of an error. On macOS and Linux it falls back
the same way, since the native window is Windows-only so far.

Two Windows details worth knowing, both of which produced real bugs:

- Windows **ignores the argument to the first `ShowWindow` call** in a process
  and uses whatever the launcher put in `STARTUPINFO`. Anything that starts the
  app minimized or hidden — a shortcut set to "Minimized", a scheduler, a
  background shell — otherwise gets a window that never appears while the app
  runs and listens invisibly, which looks exactly like a crash. It is shown a
  second time, deliberately.
- It stays a **console** program, because it has flags, an `mcp` subcommand and a
  banner carrying the phone URL and token. In desktop mode the console window is
  hidden only when this process is the sole thing attached to it — launched from
  a terminal, that terminal is yours and is left alone.

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

**A pattern match is a suspicion, not a verdict.** This is the correction of a
claim that used to sit here. Matching alone benched a perfectly healthy account
during testing, on a line of prose that merely *discussed* rate limits and
happened to be on screen — and no wording is clever enough to prevent that,
because a terminal shows file contents, fetched pages and conversation text.

So a match is confirmed by behaviour before anything irreversible happens: the
session is watched for a few seconds, and a real limit stops it dead, whereas
content that only mentions one is followed by the agent carrying straight on.
Warnings, percentages and quota panels are still ignored by construction, quoted
lines are skipped, only the tail is scanned, there is a 90-second cooldown so a
resumed conversation cannot re-trigger on its own replayed history, and the
app's own announcement is excluded. Those narrow the noise; the confirmation is
what makes acting on a match safe.

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
internal/desktop/              the native window (WebView2, pure Go)
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

181 tests in the fast suite and 15 more behind the UI tag. Covered: the cwd encoder against real transcript directories, the
cascade in every direction, provider isolation, dangling references, folder
cycles, project cascade-delete, the ring buffer's exact-wrap case, the limit
detector's true and false positives, environment filtering, credential auth
states, schedule arithmetic including short months, webhook signatures and
gating, SQL write/stacked-statement classification, vault round-trips including
"the value is not readable on disk", the MCP protocol and its project scope,
JSON salvage, the list-endpoint contract, and the browser profile's preference seeding.

Several of those exist because the code was run for real and something broke
that review had not caught. Each keeps its own evidence:

- **The pseudo-terminal must be closed exactly once.** A second
  `ClosePseudoConsole` on a closed handle ends the whole process on Windows —
  no panic, no error, nothing in the log. Two closes were reachable together, so
  pressing **Stop** killed Go AI Team and every other agent it was running.
- **Prompt delivery is verified, not assumed.** Two different things went wrong
  and looked identical: the return swallowed by bracketed paste, leaving the text
  typed and unsent; and the paste arriving while the CLI was still connecting, so
  it never landed at all. The terminal is read back to tell them apart, because
  waiting longer only makes the second rarer.
- **A limit match is confirmed before an account is benched.** See above.
- **No two UI scripts may declare the same top-level name.** They share one global
  scope, so the file that loads last silently replaces the other's helper.
- **A window must be shown twice on Windows.** The first ShowWindow call in a
  process is overridden by whatever the launcher asked for, so a shortcut set to
  "Minimized" produced an app that ran, listened, and never appeared.
- **Hiding the console window stopped working, silently.** `ShowWindow(SW_HIDE)`
  on `GetConsoleWindow()` is the classic move and it is a no-op on Windows 11:
  with Windows Terminal as the host — what "Let Windows decide" resolves to —
  the box on screen belongs to WindowsTerminal.exe and the handle you are given
  was never visible. Measured by listing every process with a visible window
  before and after. `FreeConsole` is what actually removes it, so output is
  pointed at a log file and the console is given back rather than hidden.
- **A session's id is not fixed for the life of the process.** `--resume` opens a
  picker, and choosing a conversation switches the CLI to that conversation's id.
  Reading the metafile once left the app pointing at a transcript that had never
  existed: an empty chat view and a zero token meter on a session with hours of
  history behind it.
- **A file called `api_windows.go` is a Windows-only file.** Go reads a GOOS
  name off the end of a filename with no build tag and no warning, so the
  pop-out handler — meant for every platform — left the server package unable to
  compile on Linux or macOS. It passed every check on the machine it was written
  on, which is the whole difficulty. A test now refuses any filename in that
  package ending in a GOOS or GOARCH.
- **`Terminate` posts the quit to whichever thread calls it.** The window
  binding's own comment says it is safe from a background thread; it calls
  `PostQuitMessage`, which posts to the *calling* thread's queue, so from a
  goroutine the quit lands where nobody is reading. Quit in the page and Ctrl-C
  in the terminal both did nothing to the desktop window, while the notification
  icon's Quit worked — because that one is called from inside the window
  procedure. `Dispatch` is the way across.
- **The repository is not always the project directory.** A project kept as a
  working folder — the checkout inside it, beside notes, credentials and a
  scratch script — is not a repository itself, so every git feature answered
  about the folder instead of the code in it: no diffs, an empty review pane, no
  commit messages, on a checkout with a hundred commits. git walks *up* on its
  own, which is why a project inside a repository always worked; one level down
  is now searched too, and more than one candidate is reported rather than
  guessed at. A file that lives beside the checkout says so instead of showing
  an empty pane.
- **An explanation is not a diff.** The diff endpoint answered "This project is
  not a git repository" as a 200 with a body, and the caller decided what it had
  by asking whether the body was empty. It was not, so the sentence went into
  the diff parser, which found no hunks and reported "No textual diff (binary,
  or no change)" — every file an agent had just rewritten, in any project
  without a repository, shown as unchanged. The reason was in the response the
  whole time. Answers are labelled now, and the parser refuses to read prose.
- **The split diff drew the wrong side.** The inline mark on an added line was
  taken from the line it replaced, so renaming `foo` to `bar` left the "after"
  column still saying `foo` — in the default view, on every single-line edit.
  Found by a test written for the bug above.
- **The folder-trust box is not numbered, and nothing else is like it.** Every
  other question Claude Code asks is a numbered list, so the parser required a
  digit after the cursor. The trust prompt — the first thing every new project
  meets — is a bare arrow menu, so an agent started in an unseen directory sat
  on it indefinitely, reported as merely "waiting", with nothing to click. Found
  by running a real agent, which is the only way it could have been found.
- **A transcript is read incrementally, and only its tail.** A real one here was
  257MB. Parsing it whole cost 762ms per poll and 1.24s per 1.5s refresh; now it
  is 0ms and ~100ms.
- **The end of a turn has to be announced.** The page keeps no timer of its own;
  it draws what the last event said. Token counters arrive while an agent is
  producing them and stop when it stops, so the last one before a quiet finish
  always said "working" — and with nothing emitted for the flip that followed,
  the spinner ran on the board and in the project tree until an unrelated click
  forced a reload. An agent that had finished ten minutes ago still read as busy.

`scratchpad/live_check.py` is the one that needs a real account and spends
quota: it starts an agent, answers the question it stops on, and then asks the
activity log and the transcript search whether they saw any of it. Run
deliberately, not in a loop — it is what found the trust-prompt bug above.

## Not built

Cloud agents (needs a cloud provider), a public feedback portal for clients,
Figma capture, a remote-fleet relay, and the non-Claude providers — the account
model is provider-shaped and the env vars are wired, but only Claude is exercised.
Mongo connections are saved and tunnelled but statements are not executed.

## Licence

MIT.
