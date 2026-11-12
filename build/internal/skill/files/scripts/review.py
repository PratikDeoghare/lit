#!/usr/bin/env python3
"""Review what fill.py wrote into a literate program.

    python review.py prog.lit                    # the fills waiting for review, with automatic checks
    python review.py prog.lit --all --full       # every fill ever made, with its code
    python review.py prog.lit --check 'go vet ./...' --ai --agent 'claude -p'
    python review.py prog.lit --approve f-3142cc | --approve-all | --reject f-3142cc

Each fill is checked without a model: its code is still in the program, has no placeholders, the program
still parses and tangles, generated .py/.json/.go files are valid, and --check CMD passes on the tangled
program.  --ai asks an agent for a second opinion (VERDICT: ok / concerns / reject) from the same context
fill.py used.  --reject puts the original hint back, so the gap is open again.
"""
import argparse
import json
import sys
import os

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from tangle import Source  # noqa: E402
from fill import (check_code, check_program, find_hints, load_fills, make_agent,  # noqa: E402
                  record_status, run_check, trunc)

REVIEW_SYSTEM = """You review code that an assistant wrote to fill one gap in a literate program (prose interleaved with named code chunks).
You are shown the context, the hint the author left, and the code that was inserted. Judge whether the code does what the hint and the surrounding prose ask, fits the code around it (names, types, errors, style), and could be wrong in a way a compiler or a quick test would not catch.
Answer in this exact form:
VERDICT: ok | concerns | reject
then at most six short lines of reasons, one issue per line, each starting with "- "."""


def where_is(src, fill):
    """Does the inserted text still stand in the program, exactly once?"""
    n = src.text.count(fill["inserted"])
    return n


def assess(src, fill, check="", agent=None, budget=24000):
    """-> (findings [(level, text)], ai verdict or '')"""
    fs = []
    n = where_is(src, fill)
    if n == 0:
        fs.append(("warn", "the inserted text is no longer in the program (edited since, or reverted)"))
    elif n > 1:
        fs.append(("warn", "the inserted text occurs %d times; --reject cannot tell which is the fill" % n))
    fs += check_code(fill["code"].split("\n"))
    fs += check_program(src.text)
    if check and not any(l == "error" for l, _ in fs):
        fs += run_check(src.text, check)
    verdict = ""
    if agent:
        verdict = ai_review(agent, src, fill, budget)
    return fs, verdict


def ai_review(ask, src, fill, budget):
    ctx = "Chunk: «%s» (%s line %d)\nHint: %s\n\nProse and code around the gap now:\n" % (
        fill["chunk"], fill["file"], fill["line"], fill["hint"] or "(none)")
    at = src.text.find(fill["inserted"])
    if at >= 0:
        ctx += src.text[max(0, at - budget // 4):at + len(fill["inserted"]) + budget // 8]
    user = ctx + "\n\nInserted code:\n```\n" + fill["code"] + "\n```\n\nGive your verdict."
    reply = ask(REVIEW_SYSTEM, user)
    first = next((l for l in reply.split("\n") if l.strip().upper().startswith("VERDICT")), "")
    word = first.partition(":")[2].strip().lower().split(" ")[0] if first else ""
    return word if word in ("ok", "concerns", "reject") else "concerns"


def reject(src, fill):
    n = where_is(src, fill)
    if n != 1:
        raise ValueError("cannot reject %s: its text %s" % (fill["id"], "is not in the program any more" if not n else "occurs %d times" % n))
    at = src.text.find(fill["inserted"])
    src.write_back(src.text[:at] + fill["orig"] + src.text[at + len(fill["inserted"]):])


def main(argv=None):
    ap = argparse.ArgumentParser(prog="review.py", description="Review the fills fill.py made.")
    ap.add_argument("source")
    ap.add_argument("--all", action="store_true", help="include approved and rejected fills")
    ap.add_argument("--check", default="", help="run this command on the tangled program")
    ap.add_argument("--ai", action="store_true", help="ask an agent for a second opinion")
    ap.add_argument("--agent", default="")
    ap.add_argument("--model", default="")
    ap.add_argument("--approve", metavar="ID", nargs="+")
    ap.add_argument("--approve-all", action="store_true")
    ap.add_argument("--reject", metavar="ID", nargs="+")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--full", action="store_true", help="show the inserted code")
    a = ap.parse_args(argv)

    src = Source.read(a.source)
    fills = load_fills(src)
    byid = {f["id"]: f for f in fills}

    if a.reject:
        code = 0
        for fid in a.reject:
            f = byid.get(fid)
            if not f:
                print("review: no fill %s" % fid, file=sys.stderr)
                code = 2
                continue
            src = Source.read(a.source)
            try:
                reject(src, f)
            except ValueError as e:
                print("review: %s" % e, file=sys.stderr)
                code = code or 1
                continue
            record_status(src, f["id"], "rejected")
            print("%s rejected: the hint is back in the program" % f["id"])
        return code
    if a.approve or a.approve_all:
        todo = [f for f in fills if f["status"] == "pending"] if a.approve_all else [byid.get(i) for i in a.approve]
        if None in todo:
            print("review: no such fill among %s" % " ".join(a.approve), file=sys.stderr)
            return 2
        for f in todo:
            record_status(src, f["id"], "approved")
            print("%s approved" % f["id"])
        return 0

    shown = [f for f in fills if a.all or f["status"] == "pending"]
    ask = make_agent(a.agent, a.model)[1] if a.ai else None
    results, bad = [], 0
    for f in shown:
        fs, verdict = assess(src, f, a.check, ask) if f["status"] == "pending" else ([], "")
        if verdict:
            record_status(src, f["id"], f["status"], verdict)
        bad += any(l == "error" for l, _ in fs) or verdict == "reject"
        results.append((f, fs, verdict))
    if a.json:
        print(json.dumps([{**{k: f[k] for k in ("id", "status", "chunk", "file", "line", "hint", "agent", "attempts")},
                           "code": f["code"], "findings": [{"level": l, "message": m} for l, m in fs], "verdict": v or f.get("verdict", "")}
                          for f, fs, v in results], indent=2, ensure_ascii=False))
        return 1 if bad else 0
    if not results:
        print("nothing to review" + ("" if a.all else " (--all shows approved and rejected fills too)"))
        return 0
    for f, fs, verdict in results:
        print("%s  %s  «%s»  %s:%d  [%s]" % (f["id"], f["status"], f["chunk"], f["file"], f["line"], f["agent"]))
        if f["hint"]:
            print("    hint: " + trunc(f["hint"], 100))
        if a.full:
            print("    " + f["code"].replace("\n", "\n    "))
        else:
            print("    %d line(s)" % (f["code"].count("\n") + 1))
        for l, m in fs:
            print("    %s: %s" % (l, m.replace("\n", "\n        ")))
        if verdict:
            print("    ai verdict: " + verdict)
        if not fs and verdict in ("", "ok"):
            print("    checks passed")
    print("%d fill(s) shown%s" % (len(results), "; some need attention" if bad else ""))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
