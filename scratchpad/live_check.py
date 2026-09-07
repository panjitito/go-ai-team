"""A real agent, driven through the app, on a real signed-in account.

    python scratchpad/live_check.py <state-dir> <work-dir>

This spends quota. It starts a real Claude Code process and gives it two things
to do, so run it deliberately, not in a loop.

Everything else in this repo is unit tests and a headless browser against
fixtures. This is the check CLAUDE.md asks for, and it is the one that found the
folder-trust prompt going unrecognised: an agent in a directory the CLI had not
seen sat on an unnumbered box for ever, reported as merely "waiting", with
nothing on screen to click.

Both directories must exist and be empty. The state directory is used as HOME,
so nothing here touches the real app's projects, agents or settings; the work
directory is what the agent is pointed at. The account is real, because a fake
one cannot answer anything — set ACCOUNT_DIR to a config directory that is
signed in, and not to one a Claude session is using right now.

What it does not cover: a numbered permission prompt. Measured on 2026-09-06,
this CLI asks for none — creating a file inside the trusted folder, running
`echo`, and even writing outside the project all went through silently. Those
boxes are covered by fixtures captured from real prompts, in
internal/session/testdata.
"""

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

PORT = int(os.environ.get("PORT", "7894"))
BASE = f"http://127.0.0.1:{PORT}/api"
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
EXE = os.environ.get("EXE", os.path.join(HERE, "go-ai-team.exe"))
# A real signed-in config directory. Point ACCOUNT_DIR at one of yours: this
# starts an agent there and it will spend that account's quota. Do not use the
# directory a Claude Code session is itself running in, or the two processes
# write the same credentials file.
ACCOUNT_DIR = os.environ.get(
    "ACCOUNT_DIR", os.path.join(os.path.expanduser("~"), ".claude")
)

ok = []
bad = []


def check(name, passed, detail=""):
    (ok if passed else bad).append(name)
    mark = "PASS" if passed else "FAIL"
    print(f"  [{mark}] {name}{(' — ' + detail) if detail else ''}", flush=True)
    return passed


def call(path, method="GET", body=None, timeout=30):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw = r.read().decode("utf-8", "replace")
            return json.loads(raw) if raw.strip() else {}
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"{method} {path} -> {e.code}: {e.read().decode('utf-8', 'replace')[:300]}")


def wait_for(what, fn, seconds=180, every=1.5):
    """Poll until fn() returns something truthy. Returns it, or None."""
    deadline = time.time() + seconds
    while time.time() < deadline:
        v = fn()
        if v:
            return v
        time.sleep(every)
    print(f"      (gave up waiting for {what} after {seconds}s)", flush=True)
    return None


def main():
    home = sys.argv[1]
    work = sys.argv[2]

    env = dict(os.environ, USERPROFILE=home, HOME=home)
    print(f"state dir {home}\nwork dir  {work}\naccount   {ACCOUNT_DIR}\n", flush=True)

    proc = subprocess.Popen(
        [EXE, "--port", str(PORT), "--browser", "none"],
        env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        cwd=os.path.dirname(EXE),
    )
    try:
        if not wait_for("the server", lambda: try_settings(), seconds=40):
            raise SystemExit("the server never came up")

        print("\n--- wiring it up ---", flush=True)
        acct = call("/accounts", "POST",
                    {"name": "live-check", "dir": ACCOUNT_DIR, "provider": "claude"})
        check("account attached and signed in", acct.get("signedIn"),
              f"{acct.get('name')} · {acct.get('label', '')}")

        proj = call("/projects", "POST", {"name": "live-check", "path": work})
        agent = call("/agents", "POST", {
            "projectId": proj["id"], "name": "Checker", "role": "Full-Stack",
            "accountId": acct["id"], "provider": "claude",
        })
        check("project and agent created", bool(proj.get("id") and agent.get("id")))

        print("\n--- starting the agent (this spends quota) ---", flush=True)
        sess = call(f"/agents/{agent['id']}/start", "POST", {"cols": 120, "rows": 36})
        sid = sess["id"]
        check("session started", bool(sid), sid)

        # The CLI takes a moment to be ready for input.
        wait_for("the CLI prompt", lambda: session(sid).get("status") in ("working", "waiting"), 60)
        time.sleep(6)

        # A directory the CLI has not seen before gets the folder-trust box
        # first. It is drawn with no numbers, which the ask parser could not
        # read until today — an agent would sit on it for ever.
        print("\n--- the folder-trust prompt ---", flush=True)
        trust = wait_for("the trust prompt", lambda: session(sid).get("needsYou"), 60)
        if check("the trust prompt was recognised", bool(trust),
                 (session(sid).get("question") or "")[:80]):
            conv = call(f"/sessions/{sid}/conversation?limit=3")
            opts = (conv.get("ask") or {}).get("options") or []
            check("it came back as options, not as a wall of text", len(opts) == 2,
                  " | ".join(o.get("label", "?") for o in opts))
            pick = next((i for i, o in enumerate(opts, 1)
                         if (o.get("label") or "").lower().startswith("yes")), 2)
            call(f"/sessions/{sid}/answer", "POST", {"option": pick}, timeout=60)
            check("answering it cleared it",
                  bool(wait_for("trust to clear", lambda: not session(sid).get("needsYou"), 60)))
            time.sleep(4)

        print("\n--- a prompt that has to ask permission ---", flush=True)
        # Naming the directory matters: told only "create a file", the agent uses
        # its own scratchpad, which needs no permission and nothing ever asks.
        prompt = (f"Create a file at {work}\\live-check.md containing exactly the "
                  f"line: kestrel-marker-7. Do not ask me anything else first.")
        call(f"/sessions/{sid}/input", "POST", {"data": prompt, "enter": True}, timeout=120)
        check("prompt delivered", True)

        # Writing a new file inside a folder you have just trusted does not
        # prompt, so this does not expect one here — it is the shell command
        # below that has to ask.
        wait_for("the file", lambda: os.path.exists(os.path.join(work, "live-check.md")), 180)
        wait_for("the turn to end", lambda: session(sid).get("status") == "waiting", 120)

        # Outside the trusted folder, which is what the CLI actually gates on.
        # Inside it, creating a file and running `echo` both go through without
        # asking — measured, after expecting otherwise twice.
        outside = os.path.join(os.path.dirname(work.rstrip("\\/")), "outside-check.md")
        print(f"\n--- writing outside the project, which does have to ask ---", flush=True)
        call(f"/sessions/{sid}/input", "POST",
             {"data": f"Create a file at {outside} containing the line kestrel-two.",
              "enter": True}, timeout=120)

        asking = wait_for("the permission question",
                          lambda: session(sid).get("needsYou"), 180)
        if check("the agent stopped to ask", bool(asking),
                 (session(sid).get("question") or "")[:90]):
            conv = call(f"/sessions/{sid}/conversation?limit=5")
            ask = conv.get("ask") or {}
            opts = ask.get("options") or []
            check("the question was parsed into options", len(opts) >= 2,
                  " | ".join(o.get("label", "?") for o in opts)[:120])

            # Pick the first "yes" the CLI is offering, the way a person would.
            pick = 1
            for i, o in enumerate(opts, start=1):
                if "yes" in (o.get("label") or "").lower():
                    pick = i
                    break
            print(f"      answering option {pick}", flush=True)
            call(f"/sessions/{sid}/answer", "POST", {"option": pick}, timeout=60)

            cleared = wait_for("the question to clear",
                               lambda: not session(sid).get("needsYou"), 90)
            check("answering cleared the question", bool(cleared))

        print("\n--- did it actually do the work ---", flush=True)
        target = os.path.join(work, "live-check.md")
        made = wait_for("the file", lambda: os.path.exists(target), 180)
        check("the file exists", bool(made), target)
        if made:
            body = open(target, encoding="utf-8", errors="replace").read()
            check("the file has the marker in it", "kestrel-marker-7" in body,
                  body.strip()[:60])

        print("\n--- the turn ends, and the app notices ---", flush=True)
        finished = wait_for("the turn to finish",
                            lambda: session(sid).get("status") == "waiting", 240)
        check("status flipped to waiting when it stopped", bool(finished),
              f"status={session(sid).get('status')}")

        print("\n--- what this weekend built ---", flush=True)
        acts = call("/activity?limit=100")
        kinds = [a.get("text", "") for a in acts]
        check("the activity log recorded the start",
              any(t.startswith("Started") for t in kinds), next((t for t in kinds if t.startswith("Started")), ""))
        check("the activity log recorded the question",
              any(t.startswith("Asked") for t in kinds), next((t for t in kinds if t.startswith("Asked")), "")[:80])
        check("the activity log recorded the turn ending",
              any("Finished a turn" in t for t in kinds))
        named = [a for a in acts if a.get("agentName")]
        check("activity rows name the agent", bool(named),
              named[0].get("agentName") if named else "")

        found = call("/search?q=kestrel-marker-7", timeout=60)
        hits = found.get("hits") or []
        check("search found the marker in the transcript", len(hits) > 0,
              f"{len(hits)} hits, read {found.get('files')} of {found.get('total')} "
              f"conversations in {found.get('millis')} ms")
        if hits:
            check("the hit leads back to the live agent",
                  any(h.get("liveSession") == sid for h in hits),
                  hits[0].get("agentName") or hits[0].get("sessionId", "")[:8])

        print("\n--- stopping ---", flush=True)
        call(f"/sessions/{sid}/stop", "POST", {})
        time.sleep(2)
        check("session stopped", session(sid).get("status") in ("exited", "error", None),
              str(session(sid).get("status")))

        call("/quit", "POST", {})
        time.sleep(3)
        check("the app quit when asked", proc.poll() is not None)

    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
        out = proc.stdout.read() if proc.stdout else ""
        if out.strip():
            print("\n--- server output ---\n" + out[-3000:], flush=True)

    print(f"\n{len(ok)} passed, {len(bad)} failed", flush=True)
    if bad:
        print("failed: " + ", ".join(bad), flush=True)
    return 1 if bad else 0


def try_settings():
    try:
        call("/settings", timeout=3)
        return True
    except Exception:
        return False


def session(sid):
    try:
        for s in call("/sessions", timeout=10):
            if s["id"] == sid:
                return s
    except Exception:
        pass
    return {}


if __name__ == "__main__":
    sys.exit(main())
