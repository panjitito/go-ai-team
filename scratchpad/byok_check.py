"""Bring-your-own-key, end to end, without a key and without spending anything.

    go build -o go-ai-team-dev.exe .
    go build -o scratchpad/envdump/envdump.exe ./scratchpad/envdump
    python scratchpad/byok_check.py

Unlike live_check.py this costs nothing and needs no account. It runs the real
app on a throwaway HOME, puts a value in the real vault under a throwaway name,
creates an agent pointed at an endpoint, and starts it — with claudeBin aimed at
scratchpad/envdump, which writes the environment it was handed to a file instead
of talking to anybody.

That file is the only way to answer the question this feature turns on: did the
{{secret:NAME}} reference on the agent become the actual value in the child's
environment, or did the child get the twenty-six character string? The second is
what happened before, and it fails much later and further from its cause, as an
authentication error against a provider that was configured correctly.

It also checks the two things that make the feature safe rather than merely
working: the API never hands the value back, and the session says which endpoint
it is on so the account badge cannot mislead.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

PORT = int(os.environ.get("PORT", "7896"))
BASE = f"http://127.0.0.1:{PORT}/api"
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

WIN = os.name == "nt"
EXE = os.environ.get("EXE", os.path.join(HERE, "go-ai-team-dev.exe" if WIN else "go-ai-team-dev"))
FAKE = os.path.join(HERE, "scratchpad", "envdump", "envdump.exe" if WIN else "envdump")

# A value that is obviously not a real key, and obviously ours if it turns up
# somewhere it should not.
SECRET_NAME = "BYOK_CHECK_KEY"
SECRET_VALUE = "not-a-real-key-9f3a2c"
BASE_URL = "https://api.deepseek.com/anthropic"

ok, bad = [], []


def check(name, passed, detail=""):
    (ok if passed else bad).append(name)
    print(f"  [{'PASS' if passed else 'FAIL'}] {name}{(' — ' + detail) if detail else ''}", flush=True)
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


def main():
    for path, what in ((EXE, "the app"), (FAKE, "the stand-in CLI")):
        if not os.path.exists(path):
            raise SystemExit(f"{what} is not built: {path}\nsee the docstring at the top of this file")

    home = tempfile.mkdtemp(prefix="byok-home-")
    work = tempfile.mkdtemp(prefix="byok-work-")
    dump = os.path.join(home, "child-env.json")

    env = dict(os.environ)
    env["USERPROFILE"] = home
    env["HOME"] = home
    env["ENVDUMP_OUT"] = dump
    # The hazard this feature has to survive: a shell that already exports an
    # endpoint. The agent's own setting must win over it.
    env["ANTHROPIC_BASE_URL"] = "https://inherited.example.invalid"
    env["ANTHROPIC_AUTH_TOKEN"] = "inherited-token-should-not-win"

    proc = subprocess.Popen(
        [EXE, "--port", str(PORT), "--browser", "none"],
        env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    try:
        wait_up(proc)
        print(f"app up on {PORT}, state in {home}\n")

        # --- the catalogue -------------------------------------------------
        eps = call("/endpoints")
        ids = [e["id"] for e in eps]
        check("the endpoint list is served", "deepseek" in ids and "anthropic" in ids, ", ".join(ids))

        # --- the vault -----------------------------------------------------
        vault = call("/secrets")
        if not vault.get("available"):
            print(f"\nthe vault is unavailable here: {vault.get('reason')}")
            print("nothing below can run without it.")
            return 2
        call("/secrets", "PUT", {"name": SECRET_NAME, "value": SECRET_VALUE,
                                 "note": "byok_check.py, safe to delete"})
        names = [s["name"] for s in call("/secrets")["secrets"]]
        check("the secret is stored under its name", SECRET_NAME in names)
        check("no endpoint returns the value",
              SECRET_VALUE not in json.dumps(call("/secrets")))

        # --- an agent pointed somewhere else --------------------------------
        # A real account, so the account binding and the endpoint can be seen
        # side by side. It is a directory, not a sign-in: nothing here logs in
        # and nothing here talks to a provider.
        acct_dir = os.path.join(home, "acct")
        os.makedirs(acct_dir, exist_ok=True)
        account = call("/accounts", "POST",
                       {"name": "throwaway", "dir": acct_dir, "provider": "claude"})
        call("/settings", "PATCH", {"claudeBin": FAKE})
        project = call("/projects", "POST", {"name": "byok", "path": work})
        agent = call("/agents", "POST", {
            "projectId": project["id"], "name": "Checkout", "provider": "claude",
            "accountId": account["id"],
            "env": {
                "ANTHROPIC_BASE_URL": BASE_URL,
                "ANTHROPIC_AUTH_TOKEN": "{{secret:%s}}" % SECRET_NAME,
                "MY_OWN_SETTING": "keep me",
            },
        })
        stored = [a for a in call("/agents?projectId=" + project["id"]) if a["id"] == agent["id"]][0]
        check("the saved agent stores the reference, not the key",
              stored["env"]["ANTHROPIC_AUTH_TOKEN"] == "{{secret:%s}}" % SECRET_NAME)
        check("no agent endpoint leaks the value",
              SECRET_VALUE not in json.dumps(stored))

        # --- start it, and read what the child was handed --------------------
        sess = call("/agents/%s/start" % agent["id"], "POST", {"cols": 100, "rows": 30})
        deadline = time.time() + 20
        child = None
        while time.time() < deadline:
            if os.path.exists(dump):
                with open(dump, encoding="utf-8") as f:
                    child = json.load(f)
                break
            time.sleep(0.3)
        if not check("the agent started and the child wrote its environment", child is not None):
            return 1

        check("the reference was resolved to the value in the vault",
              child.get("ANTHROPIC_AUTH_TOKEN") == SECRET_VALUE,
              repr(child.get("ANTHROPIC_AUTH_TOKEN"))[:40])
        check("the agent's endpoint beat the one exported in the shell",
              child.get("ANTHROPIC_BASE_URL") == BASE_URL,
              child.get("ANTHROPIC_BASE_URL"))
        check("a variable the form does not own came through",
              child.get("MY_OWN_SETTING") == "keep me")
        # An endpoint override changes who pays; it must not disturb which
        # account directory the CLI reads its settings and transcripts from.
        check("the account binding is still set, and is the account chosen",
              os.path.normcase(child.get("CLAUDE_CONFIG_DIR") or "") == os.path.normcase(acct_dir),
              child.get("CLAUDE_CONFIG_DIR"))

        # --- and the session says where it is talking to ----------------------
        live = [s for s in call("/sessions") if s["id"] == sess["id"]]
        endpoint = (live[0].get("endpoint") or {}) if live else {}
        check("the session reports the endpoint it is on",
              endpoint.get("host") == "api.deepseek.com", json.dumps(endpoint))
        check("it knows the choice was the agent's, not the shell's",
              endpoint.get("source") == "agent", endpoint.get("source"))
        check("no session payload carries the value",
              SECRET_VALUE not in json.dumps(call("/sessions")))

        call("/sessions/%s" % sess["id"], "DELETE")
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
