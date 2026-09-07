"""Score this repository's prose against the unslop pattern list.

    python scratchpad/slopcheck.py .                  # the whole tree
    python scratchpad/slopcheck.py . --detail         # every flag, located
    python scratchpad/slopcheck.py internal/session   # one package

Comment blocks in .go and .js, and nothing else. Code, identifiers and string
literals are left alone, and so is a legend or an ASCII diagram inside a
comment, which is not prose and does not read like it.

The README was rewritten against the same rules by hand. This asks the same
question of the code comments, and got a different answer: see the note at the
bottom of this file for what it found and why the pass stopped there.
"""

import os
import re
import sys
from collections import Counter

EXTS = (".go", ".js")
SKIP_DIRS = {".git", "vendor", "docs", "node_modules", "testdata"}

# Phrases the ruleset calls out. Deliberately narrow: each is a tic that adds no
# information, rather than a word somebody dislikes.
BANNED = [
    r"\bit'?s (?:important|worth) (?:to note|noting)\b",
    r"\bit should be noted\b",
    r"\bneedless to say\b",
    r"\bat the end of the day\b",
    r"\bwhen it comes to\b",
    r"\bin order to\b",
    r"\bplays? a (?:crucial|key|vital|pivotal) role\b",
    r"\bdelve[sd]? into\b",
    r"\bleverage[sd]?\b",
    r"\butilis[ez][sd]?\b",
    r"\bseamless(?:ly)?\b",
    r"\bcomprehensive\b",
    r"\bmyriad\b",
    r"\bin today'?s\b",
    r"\bthis (?:function|method|file) (?:is responsible for|handles the)\b",
    r"\bsimply put\b",
    r"\bthat being said\b",
    r"\bfirst and foremost\b",
]

NEGATIVE_PARALLEL = [
    r"\bis not (?:about )?[^.,;]{3,40}[,;] (?:it'?s|it is) ",
    r"\bnot (?:just|only|merely) [^.,;]{3,60}[,;] but\b",
]

NEGATIVE_RUN = re.compile(
    r"\bno [a-z][\w-]*(?:,| and) no [a-z][\w-]*(?:,| and) no [a-z][\w-]*", re.I)

CODA_TELLS = re.compile(
    r"\b(?:which is (?:the whole point|exactly)|worth knowing|"
    r"that is the (?:whole |real )?point)\b", re.I)

DASH = "—"


def comment_blocks(text):
    """Yield (start_line, block) for each run of adjacent // lines."""
    buf, start = [], None
    for i, line in enumerate(text.splitlines(), 1):
        stripped = line.strip()
        if stripped.startswith("//"):
            if start is None:
                start = i
            buf.append(stripped[2:].strip())
            continue
        if buf:
            yield start, "\n".join(buf)
            buf, start = [], None
    if buf:
        yield start, "\n".join(buf)


def paragraphs(block):
    out, cur = [], []
    for line in block.split("\n"):
        if not line:
            if cur:
                out.append(" ".join(cur))
                cur = []
            continue
        cur.append(line)
    if cur:
        out.append(" ".join(cur))
    return out


def is_prose(para):
    """A legend, a diagram or a shell line is not prose and is not scored."""
    if len(para) < 25:
        return False
    if para.count("  ") > 4:
        return False
    return not para.startswith(("go ", "$ ", "GET ", "POST ", "|"))


def dash_flag(para):
    """Em-dashes, counted the way the ruleset means them.

    Two dashes bracketing a phrase inside one sentence are a single construct
    and correct punctuation. The habit worth flagging is reaching for the dash
    again and again across separate sentences. Counting characters instead
    reported 92 pile-ups here, of which 89 were one pair each.
    """
    if para.count(DASH) < 2:
        return None
    sentences = re.split(r"(?<=[.!?])\s+", para)
    carrying = sum(1 for s in sentences if DASH in s)
    if carrying < 2:
        return None
    return "em-dash across %d sentences" % carrying


def score(text):
    flags = []
    for start, block in comment_blocks(text):
        for para in paragraphs(block):
            if not is_prose(para):
                continue
            if d := dash_flag(para):
                flags.append((start, d, para[:110]))
            low = para.lower()
            for pat in BANNED:
                if m := re.search(pat, low):
                    flags.append((start, "banned phrase %r" % m.group(0), para[:110]))
            for pat in NEGATIVE_PARALLEL:
                if m := re.search(pat, para, re.I):
                    flags.append((start, "negative parallelism", m.group(0)[:80]))
            if m := NEGATIVE_RUN.search(para):
                flags.append((start, "negative run", m.group(0)[:80]))
            if m := CODA_TELLS.search(para):
                flags.append((start, "coda tell", m.group(0)[:80]))
    return flags


def walk(root):
    if os.path.isfile(root):
        yield root
        return
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            if fn.endswith(EXTS):
                yield os.path.join(dirpath, fn)


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    detail = "--detail" in sys.argv

    found = []
    paras = 0
    files = 0
    for path in walk(root):
        files += 1
        with open(path, encoding="utf-8", errors="replace") as f:
            text = f.read()
        for _, block in comment_blocks(text):
            paras += sum(1 for p in paragraphs(block) if is_prose(p))
        rel = os.path.relpath(path, root if os.path.isdir(root) else ".")
        found.extend((rel, line, kind, snip) for line, kind, snip in score(text))

    print("%d flags across %d comment paragraphs in %d files" % (len(found), paras, files))
    for kind, n in Counter(f[2].split(" (")[0].split(" %r")[0] for f in found).most_common():
        print("  %-32s %d" % (kind, n))

    if detail:
        print()
        for path, line, kind, snip in found:
            print("%s:%d  %s\n    %s" % (path, line, kind, snip))
    return 0


if __name__ == "__main__":
    sys.exit(main())

# What it said on 2026-09-07, over 2,742 comment paragraphs in 185 files:
#
#   21 flags. 13 coda tells, 3 negative runs, 2 uses of negative parallelism,
#   3 paragraphs reaching for the em-dash in more than one sentence.
#
# The answer was to stop, so this file is a measurement rather than a to-do
# list. The README needed the pass because it was written to persuade: 77
# em-dashes in 60 paragraphs, more than one each, and seven paragraphs ending
# on an aphorism. Comments here are written to explain a specific bug and they
# name specific things, so the patterns the ruleset hunts for mostly are not in
# them.
#
# The em-dash number is the clearest case. Counting the character found 92
# paragraphs with two or more, which looks like a habit until you split them by
# sentence: 89 were one pair bracketing a phrase, which is what the punctuation
# is for. Three were not, and two of those are a legend and a quoted question.
#
# The codas went the same way. Most are doing real work: "billed separately,
# which is why they are never summed into one number" states a cause. Three
# sites were genuinely empty and were fixed. Rewriting the rest would have cost
# information to satisfy a metric, and the comments in this repository are
# load-bearing, as CLAUDE.md says at some length.
