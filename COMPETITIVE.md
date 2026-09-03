# AgentsRoom: what it is, and where Go AI Team stands

Research notes from a full crawl of `agentsroom.dev` (159 English pages, all 70+
feature pages read). This is the map we are building against.

## What AgentsRoom is

An Electron desktop app that wraps agent CLIs in a GUI. Every agent is a card
with live output, a role label and a status dot; a sidebar holds projects; the
terminal is xterm.js. It positions itself as an "agentic IDE" — the unit of work
is the agent, not the cursor — and coexists with your real editor rather than
replacing it.

Built with **Electron + xterm.js**. macOS, Windows, Linux. A separate native
iOS/Android companion app talks to the desktop over an encrypted relay.

### Pricing (the reason we are here)

| Plan | Price | |
|---|---|---|
| Free | €0 | 3 projects, 6 parallel agents, **extra CLI accounts: none** |
| Plus | earned | 5 projects, 9 agents. Given for a public post or a popular published agent |
| Pro | **€7.99/mo** or €75/yr | unlimited projects and agents, all 14 providers, multiple CLI accounts |
| Team | €12.99/seat/mo, min 3 seats | Pro for everyone, one invoice, seat admin |

The marketing page for Claude multi-account says it is "included in the free
tier". The pricing table says `Extra CLI accounts: None` on Free and `Multiple`
on Pro. **Multi-account is a Pro feature** — that is the €7.99/month wall this
project exists to remove.

Caps are metered per month (100 AI suggestions, 5 commit messages, 3 summaries,
2h voice, 1,200 cloud minutes on Pro…). You can attach your own OpenAI key to
keep metered features working past a cap, billed to you.

## The full feature surface

Grouped as the site groups it. This is the roadmap ordered by their bet on what
matters.

**Fleet and orchestration**
- Multi-project / multi-agent grid; colour-coded roles; working/done counters
- Split view — N-way drag-and-drop tiling, pinned panes, layout restored per project
- Agent Teams — n8n-style visual workflow canvas, conditional edges, feedback loops, max-cycles guard
- Agent messaging — saved agents get an address and an inbox; messages persisted to disk before delivery, with read receipts
- Agent morphing — change a running agent's role in place, keeping full context; "fresh eyes" option; morph trail
- Fork agent — twin with the whole conversation (native session fork on Claude/Codex/Grok); read-only "Ask" bubble
- Agent delegation — dev agent hands browser validation to an ephemeral cheap-model QA agent over MCP
- Agent suggestions — describe a task, get the best-fit agent with a one-line reason
- Remote fleet — several machines in one window over an E2EE relay
- Cloud agents — disposable cloud VM clones the repo, runs headless, pushes a branch, self-destructs

**Agents catalogue**
- 14 built-in roles (Architect, Full-Stack, Frontend, Backend, Mobile, DevOps, QA, Security, PM, Marketing, Git, SEO, Localization, Brainstormer) each pinned to a model tier
- 273 expert agents from "The Agency" (MIT-licensed), across 16 divisions
- A community marketplace; publish your own; share one by dragging it into chat

**Accounts and providers**
- Multi-account for Claude / Codex / Grok / Cursor, one env var each: `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `GROK_HOME`, `CURSOR_CONFIG_DIR`
- Cascade: agent override → project pin → nearest folder pin → global default → `~/.claude`
- In-app sign-in via a mini PTY; polls `.credentials.json` once a second
- CCS (Claude Code Switcher) profile compatibility
- Account auto-switch on quota exhaustion, carrying the transcript across
- 14 providers: Claude, Codex, Copilot CLI, OpenCode, Antigravity, Aider, Grok Build, Mistral Vibe, Kimi, Amp, oh-my-pi, Freebuff, Devin, Cursor
- Per-agent CLI flags and env vars; live model switch on Claude; handoff summary on provider change

**Work intake**
- Backlog kanban — drag a task to In Progress and an agent starts
- Public backlog — client-facing portal, upvotes, comments, widget + Chrome extension
- Ticket scoping — a PM agent reads the codebase and builds a mockup before any code
- Idea Radar — clusters raw feedback into themes; impact/effort matrix; opportunity tree; semantic map
- Prompt library with folders, git sharing, gitignored personals; linked prompts that compose
- Skills library — `SKILL.md`, trigger-based routing, export to Cursor/Codex/Windsurf/Aider
- Project memory — agents write decisions/pitfalls/conventions that survive the session
- Figma frame capture straight onto a ticket

**Monitoring and cost**
- Live per-session Claude token meter: input, output, cache write/read, cache hit rate, tool uses, models routed; red on heavy use; contextual tips with one-click fixes
- Quota gauge that follows the right account; optional per-project/per-agent split of the published percentage
- Project statistics — time open, active vs idle, prompts, tokens, cost
- Context drift detection ("canary") — warns when an agent stops updating its status line for two turns
- Process Guard — sweeps agent child processes, flags ones that are large AND old AND idle on CPU
- Adaptive mode — reads the prompt and suggests the cheapest adequate model

**Review**
- Per-agent diff filtering; vibe-coding file-by-file review
- Commit context — attaches the agent conversation to each commit as an unlisted gist, linked from the message
- AI commit messages from the real diff, in Conventional Commits

**Environment**
- Dev terminals — saved per-project commands, detachable window, launch from mobile
- Restore session — agents, terminals and working dirs restored on next launch
- Localhost tunnel with custom subdomain; SSH connection manager; secret vault via OS keychain (agents can name a secret but never read it); DB client read-only by default; AWS SSM port-forward to RDS in a private subnet
- CLI Doctor — explains why an agent CLI failed to launch
- GitHub/GitLab repo import
- Four MCP servers exposing the app itself to agents (Backlog, Terminal Commands, Prompt Library, Browser)

**Input and I/O**
- Sketch canvas, screenshot-to-agent shortcut, voice dictation (19 languages), two-way voice mode, read-aloud with condensed AI summary, message queue, scratchpad, dynamic island, scheduled tasks (cron without cron), webhook triggers with signature verification and burst limits

## Where Go AI Team stands

**Done, and verified against two real accounts on this machine:** the account
model, the full cascade, in-app sign-in, attach/discover existing directories,
live PTY terminals, the token meter, quota detection with auto-switch, the shared
user layer, honest auth state, doctor, phone access.

We match their multi-account feature and go past it in four places:

1. **One 12 MB binary**, not a ~250 MB Electron app. No `npm install`.
2. **The browser is the mobile app.** `--host 0.0.0.0` and the same UI works on a
   phone, gated by a per-run token. They ship and maintain a separate native app.
3. **The account badge is verified against disk**, not just intended — once
   Claude writes its own session file we show the account it actually used.
4. **Sign-in state is read, not assumed.** A logged-out `.credentials.json` is a
   husk with blank tokens; treating its existence as proof makes the dashboard
   lie and makes auto-switch hand a live conversation to a dead account. See
   README.

Two bugs that only real testing could have found, both now fixed and pinned by
tests: the husk problem above, and inherited `CLAUDE_CODE_CHILD_SESSION` turning
transcript writing off — which would have left the token meter permanently empty
whenever the app was launched from inside a Claude session.

## Suggested build order from here

Cheapest-to-most-valuable, given the account core is done:

1. **Split view** — pure UI over the session manager we already have; their most
   distinctive fleet feature.
2. **Restore session** — we already persist everything needed; one checkbox.
3. **Multi-provider** — the store is already provider-shaped (`EnvVar()`,
   `Bin()`); this is wiring plus a model picker, not a redesign.
4. **Backlog kanban + MCP server** — the piece that makes agents drive the app
   instead of only being driven by it. Their real moat.
5. **Scheduled tasks / webhook triggers** — high value with auto-switch already
   in place, because an unattended 2am run no longer dies at a quota wall.
6. **Process Guard** — cheap to build, and the measured win they quote (15.5 GB
   returned on a 16 GB laptop) is real.

Deliberately last: voice, the 273-agent catalogue (it is MIT, so it is an import
not a build), cloud agents, and the client portal.
