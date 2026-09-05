# Working on Go AI Team

## Never drive the user's own browser profile

Do not attach a debugger to, navigate, or automate the user's everyday Chrome
profile. It has their personal and work Google accounts signed into it, and a
"Claude started debugging this browser" banner across a window they are using is
both intrusive and a real risk to accounts that are not yours to touch.

The same goes for the app's own profile at `~/.goaiteam/browser`: that one is the
product, not a fixture, and stepping through it leaves state behind.

**To check the UI, run the test that owns its own browser:**

```bash
go build -o go-ai-team.exe .
go test -tags uitest ./internal/server/ -run TestUI -v
```

`internal/server/uicheck_test.go` launches headless Chrome on a throwaway
`t.TempDir()` profile, speaks CDP over a websocket, walks every tab and panel,
and asserts nothing crashed and the layout has not blown out. It is behind the
`uitest` build tag so ordinary `go test ./...` stays fast and needs no browser.

If a check does not exist yet, add it there rather than reaching for a browser
extension.

## Verifying changes

- `gofmt -l .` and `go vet ./...` must be clean.
- `go test ./...` — the fast suite, no browser, no network.
- `go test -tags uitest ./internal/server/ -run TestUI` — the UI smoke test.
- `scratchpad/loop_api.py` and `scratchpad/loop_auto.py` (if present) exercise a
  running server end to end, including the failure paths.

Run the app against a real signed-in account before claiming a feature works.
Several bugs in this codebase's history passed review and only showed up when
something was actually run — a PTY closing before its output drained, a nil
slice serialising as `null`, a credentials file that exists but holds no tokens.

## House style

- Comments explain *why*, especially where the obvious approach is wrong. There
  are several of those here and each one is load-bearing.
- Errors say what to do next, in plain language.
- A list endpoint always returns `[]`, never `null` — see `internal/server/lists.go`.
- Nothing is killed, deleted or uploaded without the user asking.
- No credential is ever returned by the API, logged, or put in a prompt.

## Closing a pseudo-terminal twice ends the process

On Windows a pty is a pseudoconsole, and calling `ClosePseudoConsole` on a
handle that is already closed terminates the whole process instantly — no panic,
no error return, no signal, nothing written to the log. It looks exactly like the
app being killed from outside.

This actually happened: `Stop` closed the pty, the read loop then hit EOF and
closed it again on its way out, and pressing **Stop** in the UI killed Go AI Team
along with every other agent it was running. `Session.closePTY` now guards it
with a `sync.Once`. Never add another `pty.Close()` call — route it through
`closePTY`.

## The first ShowWindow call is not yours

Windows ignores the argument to the *first* `ShowWindow` in a process and uses
whatever the launcher put in `STARTUPINFO`. Anything that starts the app
minimized or hidden — a shortcut set to "Minimized", a scheduler, a background
shell — then gets a window that never appears while the app runs and listens
invisibly. It looks exactly like a crash.

`internal/desktop` shows the window a second time on purpose. Do not "tidy" that
away.

## Shell gotcha

Writing Go or JS source through a bash heredoc into Python mangles backslash
escapes: `\n` and `\x1b` end up as real bytes inside string literals and break
the file. Use the Write or Edit tools for content containing escapes.

## `open(path, 'w')` truncates before the write is evaluated

This one-liner pattern for scripted edits has destroyed a source file in this
repo:

```python
io.open(p, 'w').write(s.replace(old, new))   # never do this
```

Python opens the file — truncating it to zero — and *then* evaluates the
argument. If that expression raises for any reason (a typo in a variable name
did it here), the file is already empty and the exception looks like the edit
simply did not happen. `node --check` passes on an empty file, so the next thing
that runs may well look fine too.

Build the string first, assert on it, and only then open the file. Better still,
use the Edit tool, which cannot half-apply.

## Do not edit prose with perl or sed

Multi-line `perl -0pi -e 's|...|...|'` over Markdown has silently welded table
rows onto the document title in this repo twice, and both times it was committed
before anyone looked. The pattern matches across the newline, the replacement
loses it, and nothing errors.

Use the Write or Edit tools for prose and for anything with escapes. `sed` on a
single well-anchored line is fine; a multi-line pattern is not.
