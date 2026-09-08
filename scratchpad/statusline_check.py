"""The usage strip, end to end, on a real server, without spending anything.

    go build -o go-ai-team-dev.exe .
    go build -o scratchpad/envdump/envdump.exe ./scratchpad/envdump
    python scratchpad/statusline_check.py

The five-hour and weekly figures are read off the terminal, because they exist
nowhere else: a transcript carries token counts per message and no rate limits
at all. That scrape had two failure modes and this exercises both.

An account with no status-line command prints no usage figures, so the strip was
blank for ever with nothing saying why. Here the API is asked what it says about
an account in that state, then the app's own status line is installed and the
answer changes.

And a scrape that misses used to erase a reading that was right a moment ago,
which is what made the strip appear and disappear on its own. The stand-in CLI
prints a status line and then forty kilobytes of output on top of it, exactly
like a tool dumping a build log, and the reading has to survive that.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

PORT = int(os.environ.get("PORT", "7897"))
BASE = f"http://127.0.0.1:{PORT}/api"
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

WIN = os.name == "nt"
EXE = os.environ.get("EXE", os.path.join(HERE, "go-ai-team-dev.exe" if WIN else "go-ai-team-dev"))
FAKE = os.path.join(HERE, "scratchpad", "envdump", "envdump.exe" if WIN else "envdump")

STATUS_LINE = "proj Opus 5 ctx:42% $1.23 5h:13% wk:34%"

ok, bad = [], []


def check(name, passed, detail=""):
    (ok if passed else bad).append(name)
    print(f"  [{'PASS' if passed else 'FAIL'}] {name}{(' - ' + detail) if detail else ''}", flush=True)
    return passed


def call(path, method="GET", body=None, timeout=30):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        raw = r.read().decode()
    return json.loads(raw) if raw.strip() else None


def wait_up(proc, seconds=25):
    deadline = time.time() + seconds
    while time.time() < deadline:
        if proc.poll() is not None:
            raise SystemExit(f"the app exited early with code {proc.returncode}")
        try:
            call("/settings", timeout=3)
            return
        except Exception:
            time.sleep(0.3)
    raise SystemExit("the app never came up")


def conversation(sess_id):
    return call(f"/sessions/{sess_id}/conversation?limit=1")


def wait_for(sess_id, want, seconds=25):
    """Poll the conversation until want(d) is true, then return that answer."""
    deadline = time.time() + seconds
    last = None
    while time.time() < deadline:
        last = conversation(sess_id)
        if want(last):
            return last
        time.sleep(0.5)
    return last


def main():
    for path, what in ((EXE, "the app"), (FAKE, "the stand-in CLI")):
        if not os.path.exists(path):
            raise SystemExit(f"{what} is not built: {path}\nsee the docstring at the top of this file")

    home = tempfile.mkdtemp(prefix="sl-home-")
    work = tempfile.mkdtemp(prefix="sl-work-")

    env = dict(os.environ)
    env["USERPROFILE"] = home
    env["HOME"] = home
    env["ENVDUMP_STATUSLINE"] = STATUS_LINE
    # Enough to bury the line well past the near window, after a pause long
    # enough for it to have been read once.
    env["ENVDUMP_NOISE"] = "60000"
    env["ENVDUMP_NOISE_DELAY"] = "4"

    proc = subprocess.Popen(
        [EXE, "--port", str(PORT), "--browser", "none"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    try:
        wait_up(proc)
        print(f"app up on {PORT}, state in {home}\n")

        acct_dir = os.path.join(home, "acct")
        os.makedirs(acct_dir, exist_ok=True)
        account = call("/accounts", "POST",
                       {"name": "throwaway", "dir": acct_dir, "provider": "claude"})

        # --- an account with no status line at all -------------------------
        sl = call(f"/accounts/{account['id']}/statusline")
        check("an account with no status line says so",
              sl["configured"] is False and sl["ours"] is False)
        check("it also says what it would write",
              "statusline" in (sl.get("wouldInstall") or ""), sl.get("wouldInstall"))

        # --- installing, and taking it back out ----------------------------
        settings = os.path.join(acct_dir, "settings.json")
        with open(settings, "w", encoding="utf-8") as f:
            json.dump({"model": "opus", "includeCoAuthorBy": False}, f)

        call(f"/accounts/{account['id']}/statusline", "PUT")
        with open(settings, encoding="utf-8") as f:
            after = json.load(f)
        check("installing writes a status-line command",
              isinstance(after.get("statusLine"), dict)
              and "statusline" in after["statusLine"].get("command", ""),
              json.dumps(after.get("statusLine")))
        check("and leaves the rest of the file alone",
              after.get("model") == "opus" and after.get("includeCoAuthorBy") is False)

        call(f"/accounts/{account['id']}/statusline", "DELETE")
        with open(settings, encoding="utf-8") as f:
            after = json.load(f)
        check("removing takes it out again", "statusLine" not in after)
        check("and still leaves the rest", after.get("model") == "opus")

        # --- the command itself --------------------------------------------
        payload = json.dumps({
            "workspace": {"current_dir": work},
            "model": {"display_name": "Opus 5"},
            "context_window": {"used_percentage": 42},
            "cost": {"total_cost_usd": 1.23},
            "rate_limits": {"five_hour": {"used_percentage": 13},
                            "seven_day": {"used_percentage": 34}},
        })
        printed = subprocess.run([EXE, "statusline"], input=payload.encode(),
                                 capture_output=True).stdout.decode().strip()
        check("the statusline subcommand prints the figures",
              "5h:13%" in printed and "wk:34%" in printed and "ctx:42%" in printed, printed)

        # --- what the strip says with nothing to read -----------------------
        call("/settings", "PATCH", {"claudeBin": FAKE})
        project = call("/projects", "POST", {"name": "sl", "path": work})
        agent = call("/agents", "POST", {
            "projectId": project["id"], "name": "Probe", "provider": "claude",
            "accountId": account["id"],
        })
        sess = call(f"/agents/{agent['id']}/start", "POST", {"cols": 100, "rows": 30})

        # The stand-in prints the line straight away, so this should arrive.
        d = wait_for(sess["id"], lambda d: d and (d.get("line") or {}).get("hasFiveHour"))
        line = (d or {}).get("line") or {}
        if not check("the five-hour figure is read off the terminal",
                     line.get("hasFiveHour") and line.get("fiveHour") == 13,
                     json.dumps(line)):
            return 1
        check("so are the weekly, context and cost figures",
              line.get("weekly") == 34 and line.get("context") == 42
              and abs(line.get("cost", 0) - 1.23) < 0.001,
              json.dumps(line))
        check("a fresh reading is not marked stale", line.get("ageSeconds") == 0,
              str(line.get("ageSeconds")))
        check("nothing is explained while there are numbers to show",
              not (d or {}).get("lineNote"), (d or {}).get("lineNote"))

        # --- and now bury it under a build log ------------------------------
        print("\n  waiting for the stand-in to bury the line under 60 KB...", flush=True)
        d = wait_for(sess["id"],
                     lambda d: d and ((d.get("line") or {}).get("ageSeconds") or 0) > 0,
                     seconds=40)
        line = (d or {}).get("line") or {}
        check("the reading survives being buried",
              line.get("hasFiveHour") and line.get("fiveHour") == 13, json.dumps(line))
        check("and says how old it is rather than looking current",
              (line.get("ageSeconds") or 0) > 0, str(line.get("ageSeconds")))

        call(f"/sessions/{sess['id']}", "DELETE")

        # --- the blank strip, explained -------------------------------------
        env2_agent = call("/agents", "POST", {
            "projectId": project["id"], "name": "Quiet", "provider": "claude",
            "accountId": account["id"],
            "env": {"ENVDUMP_STATUSLINE": "", "ENVDUMP_NOISE": "0"},
        })
        quiet = call(f"/agents/{env2_agent['id']}/start", "POST", {"cols": 100, "rows": 30})
        d = wait_for(quiet["id"], lambda d: d and d.get("lineNote"), seconds=20)
        check("a strip with nothing to show explains itself",
              bool((d or {}).get("lineNote")), (d or {}).get("lineNote"))
        check("and says installing one is the answer",
              (d or {}).get("lineFixable") is True)
        call(f"/sessions/{quiet['id']}", "DELETE")
    finally:
        try:
            call("/quit", "POST", timeout=3)
        except Exception:
            pass
        time.sleep(0.6)
        if proc.poll() is None:
            proc.kill()
        for d in (home, work):
            shutil.rmtree(d, ignore_errors=True)

    print(f"\n{len(ok)} passed, {len(bad)} failed")
    if bad:
        print("failed: " + ", ".join(bad))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
