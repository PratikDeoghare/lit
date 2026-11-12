#!/usr/bin/env python3
"""Reconcile a .lit with the files tangled from it, in both directions, without losing either side's edits.

    python sync.py SOURCE.lit COMMAND [-o OUTDIR] ...

    status [--check]               what changed on each side since the last sync (--check: exit 1 if anything)
    diff [FILE ...]                every region: made in the .lit / made in a generated file / conflict
    merge [--prefer lit|gen|newer] [--dry-run] [--force]
                                   bring every non-conflicting change across, both ways; conflicts are left
                                   pending (exit 1) unless --prefer decides them
    pick [FILE ...] [--force] [--yes]
                                   choose region by region, interactively (like `git add -p`, both ways)
    log [-n N] [--path P]          the journal: edits watch.py saw, backups, syncs
    restore PATH [--list | --sha S | --stdout]
                                   bring back a recorded version of a generated file or of the .lit
    adopt (--base-lit OLD.lit | --gen-is-base)
                                   start from a project tangled before sync existed

How it works (details in litsync.py): the generated files at the last sync are the base.  The .lit side is
tangle(.lit), the other side is the files on disk.  A three-way merge in generated-file space sorts every
changed region into "only the .lit changed it", "only the file changed it", "both, identically" and
"conflict".  A change that crosses from the .lit regenerates the file; one that crosses from a file is folded
into the .lit through the sourcemap (as absorb.py does).  What you leave alone stays pending: it is still a
change next time.  Everything replaced is kept as a snapshot (see `log`, `restore`).

Interactive keys for a change made on one side:
    y  apply it to the other side         n  leave it pending          r  revert it where it was made
    e  edit the text, then apply to both  a  apply all remaining in this file     A  apply all remaining
    d  done: leave everything else pending                 p  previous      q  quit, change nothing
For a conflict:
    l  take the .lit's version   g  take the file's version   b  both (.lit's then file's)
    e  edit   r  revert both to the base   n  leave pending   d / p / q as above

Edits you make with `e` start from the .lit's version (conflicts) or the changed text; $VISUAL / $EDITOR is used.
"""
import argparse
import difflib
import os
import shlex
import subprocess
import sys
import tempfile
import time

import sourcemap
from absorb import read_disk, tangle_text, to_lines
from litsync import (APPLY, BOTH, CUSTOM, LEAVE, REVERT, TAKE_GEN, TAKE_LIT, Analysis, Store, commit, prepare, sha)
from sourcemap import MapError
from tangle import Source

COLOR = sys.stdout.isatty() and "NO_COLOR" not in os.environ
paint = lambda s, code: f"\033[{code}m{s}\033[0m" if COLOR else s

LABEL = {"lit": "made in the .lit; the generated file does not have it",
         "gen": "made in the generated file; the .lit does not have it",
         "same": "the same change on both sides",
         "conflict": "CONFLICT: both sides changed these lines differently"}


class Quit(Exception):
    pass


# ---------------------------------------------------------------- display
def where(r):
    return f"base lines {r.s + 1}-{r.e}" if r.e > r.s else f"inserted after base line {r.s}"


def show_region(fs, i, ctx=3):
    r = fs.regions[i]
    kind = {"lit": "36", "gen": "33", "same": "32", "conflict": "31;1"}[r.kind]
    print(paint(f"\n== {fs.name}  region {i + 1}/{len(fs.regions)}  ({where(r)})  {LABEL[r.kind]}", kind))
    for n in range(max(0, r.s - ctx), r.s):
        print(f"   {n + 1:4d} | {fs.base[n]}")
    if r.kind == "conflict":
        print(paint("<<<<<<< .lit side", "36"))
        print("\n".join(r.lit) if r.lit else paint("(nothing)", "2"))
        print("||||||| base")
        print("\n".join(r.base) if r.base else paint("(nothing)", "2"))
        print(paint("======= generated file", "33"))
        print("\n".join(r.gen) if r.gen else paint("(nothing)", "2"))
        print(">>>>>>>")
    else:
        new = r.lit if r.kind in ("lit", "same") else r.gen
        for line in r.base:
            print(paint(f" -     | {line}", "31"))
        for line in new:
            print(paint(f" +     | {line}", "32"))
    for n in range(r.e, min(len(fs.base), r.e + ctx)):
        print(f"   {n + 1:4d} | {fs.base[n]}")


def lit_diff(an, new):
    """What the .lit would look like after the change, one diff per file of the program."""
    changed = an.src.split_back(new)
    return "\n".join("\n".join(difflib.unified_diff(an.src.texts[n].split("\n"), t.split("\n"), f"a/{n}", f"b/{n}",
                                                    n=1, lineterm="")) for n, t in sorted(changed.items()))


def describe(an, out, decisions=None):
    if out.lit_changed:
        print(paint("\nthe .lit would change:", "1"))
        print(lit_diff(an, out.new_lit))
    else:
        print("\nthe .lit would not change")
    rewrite = [n for n, t in out.gen_text.items()
               if an.files[n].missing or to_lines(read_disk(os.path.join(an.outdir, n))) != to_lines(t)]
    print("generated files rewritten: " + (", ".join(rewrite) if rewrite else "none"))
    left = sum(1 for k, (a, _) in (decisions or {}).items() if a == LEAVE)
    unresolved = sum(1 for n, fs in an.files.items() for i, r in enumerate(fs.regions)
                     if r.kind == "conflict" and (n, i) not in (decisions or {}))
    if left or unresolved:
        print(f"left pending: {left + unresolved} region(s)")
    for note in out.notes:
        print("note: " + note)


def report_problems(out):
    for e in out.errors:
        print(paint("cannot sync: " + e, "31"))
    for n, (want, got) in out.ripple.items():
        print(paint(f"side effect in {n}: the patched .lit regenerates it differently from what was chosen "
                    "(a chunk used in several places)", "33"))
        for line in list(difflib.unified_diff(want, got, "chosen", "regenerated from the .lit", n=0, lineterm=""))[:12]:
            print("    " + line)


# ---------------------------------------------------------------- interactive
def ask(prompt, valid):
    while True:
        try:
            ans = input(prompt).strip()
        except EOFError:
            raise Quit()
        if ans and ans[0] in valid:
            return ans[0]
        print("  choose one of: " + " ".join(valid) + "   (? for help)")


def edit_text(lines):
    editor = os.environ.get("VISUAL") or os.environ.get("EDITOR") or "vi"
    with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False, encoding="utf-8") as f:
        f.write("\n".join(lines) + ("\n" if lines else ""))
    try:
        subprocess.call(shlex.split(editor) + [f.name])
        return to_lines(read_disk(f.name))
    finally:
        os.unlink(f.name)


def pick(an, names, yes=False, force=False):
    todo = [(n, i) for n in names for i in range(len(an.files[n].regions))]
    if not todo:
        print("nothing to choose: both sides agree.")
        return 0
    decisions, k, auto = {}, 0, None
    while k < len(todo):
        n, i = todo[k]
        fs = an.files[n]
        r = fs.regions[i]
        if auto and r.kind != "conflict" and auto in ("ALL", n):
            decisions[(n, i)] = (APPLY, None)
            k += 1
            continue
        show_region(fs, i)
        if r.kind == "conflict":
            keys = "lgbernpdq?"
            question = "[l]it's  [g]en's  [b]oth  [e]dit  [r]evert  [n]ext/leave  [p]rev  [d]one  [q]uit  [?] > "
        else:
            keys = "ynreaApdq?"
            to = "the generated file" if r.kind == "lit" else "the .lit" if r.kind == "gen" else "both"
            question = f"apply to {to}? [y]es [n]o/leave [r]evert [e]dit [a]ll in file [A]ll [p]rev [d]one [q]uit [?] > "
        try:
            c = ask(question, keys)
        except Quit:
            print("\nquit: nothing changed.")
            return 1
        if c == "?":
            print(__doc__.split("Interactive keys")[1].split("Edits you make")[0])
            continue
        if c == "q":
            print("quit: nothing changed.")
            return 1
        if c == "p":
            k = max(0, k - 1)
            continue
        if c == "d":
            for n2, i2 in todo[k:]:
                decisions.setdefault((n2, i2), (LEAVE, None))
            break
        if c in "yn" and r.kind != "conflict":
            decisions[(n, i)] = (APPLY, None) if c == "y" else (LEAVE, None)
        elif c == "n":
            decisions[(n, i)] = (LEAVE, None)
        elif c == "r":
            decisions[(n, i)] = (REVERT, None)
        elif c == "l":
            decisions[(n, i)] = (TAKE_LIT, None)
        elif c == "g":
            decisions[(n, i)] = (TAKE_GEN, None)
        elif c == "b":
            decisions[(n, i)] = (BOTH, None)
        elif c == "e":
            start = r.lit if r.kind in ("lit", "same", "conflict") else r.gen
            decisions[(n, i)] = (CUSTOM, edit_text(start))
        elif c == "a":
            auto = n
            decisions[(n, i)] = (APPLY, None)
        elif c == "A":
            auto = "ALL"
            decisions[(n, i)] = (APPLY, None)
        k += 1
    out = prepare(an, decisions)
    return finish(an, out, decisions, yes, force, interactive=True)


# ---------------------------------------------------------------- applying
def finish(an, out, decisions, yes, force, interactive=False, dry=False):
    describe(an, out, decisions)
    report_problems(out)
    if out.errors:
        return 1
    if out.ripple and not force:
        if not interactive:
            print("\nnothing written. Make the same edit in every copy of the chunk, or pass --force to let the "
                  ".lit's version win.")
            return 1
        try:
            if ask("\nAccept these side effects (the .lit wins)? [y/n] > ", "yn") != "y":
                print("nothing written.")
                return 1
        except Quit:
            print("nothing written.")
            return 1
    if dry:
        print("\n(dry run: nothing written)")
        return 0
    if interactive and not yes:
        try:
            if ask("\nWrite these changes? [y/n] > ", "yn") != "y":
                print("nothing written.")
                return 1
        except Quit:
            print("nothing written.")
            return 1
    written = commit(an, out, note="sync")
    print(f"\nsynced. .lit {'updated' if out.lit_changed else 'unchanged'}; wrote: "
          + (", ".join(written) if written else "no generated files") + ". Previous versions are kept (see `log`).")
    return 0


def newer(an, name):
    st = an.store
    t_lit = max([st.last_edit("lit", n) or os.path.getmtime(os.path.join(an.src.base, n)) for n in an.src.order])
    path = os.path.join(an.outdir, name)
    t_gen = st.last_edit("gen", name) or (os.path.getmtime(path) if os.path.exists(path) else 0)
    return "lit" if t_lit >= t_gen else "gen"


def merge(an, prefer, dry, force):
    decisions, undecided = {}, 0
    for n, fs in an.files.items():
        for i, r in enumerate(fs.regions):
            if r.kind != "conflict":
                continue
            side = newer(an, n) if prefer == "newer" else prefer
            if side == "lit":
                decisions[(n, i)] = (TAKE_LIT, None)
            elif side == "gen":
                decisions[(n, i)] = (TAKE_GEN, None)
            else:
                undecided += 1
    if not any(fs.regions for fs in an.files.values()) and not any(fs.missing for fs in an.files.values()):
        print("nothing to merge: both sides agree.")
        return 0
    out = prepare(an, decisions)
    rc = finish(an, out, decisions, yes=True, force=force, dry=dry)
    if rc == 0 and undecided:
        print(paint(f"{undecided} conflict(s) left pending; resolve with `pick` or `merge --prefer ...`", "33"))
        return 1
    return rc


# ---------------------------------------------------------------- other commands
def status(an, check):
    pending = 0
    if an.lit_changed():
        print("the .lit has changed since the last sync")
    for n, fs in an.files.items():
        c = {k: sum(1 for r in fs.regions if r.kind == k) for k in ("lit", "gen", "same", "conflict")}
        if not fs.regions and not fs.missing:
            print(f"  {n:28s} in sync")
            continue
        pending += len(fs.regions) + fs.missing
        bits = [f"{c['lit']} from the .lit" if c["lit"] else "", f"{c['gen']} edited in the file" if c["gen"] else "",
                f"{c['same']} identical" if c["same"] else "", paint(f"{c['conflict']} CONFLICT", "31") if c["conflict"] else "",
                "file missing" if fs.missing else ""]
        print(f"  {n:28s} " + ", ".join(b for b in bits if b))
    if pending:
        print(f"\n{pending} thing(s) to reconcile: python sync.py {an.source} merge   or   ... pick")
    else:
        print("\neverything is in sync.")
    return 1 if (check and pending) else 0


def show_log(store, n, path):
    evs = [e for e in store.events() if not path or e["path"] == path]
    for e in evs[-n:]:
        when = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(e["t"]))
        extra = e.get("note") or (", ".join(e["files"]) if e.get("files") else "")
        print(f"{when}  {e['kind']:9s} {e['side']:4s} {e['path']:28s} {(e['sha'] or '-')[:8]}  {extra}")
    if not evs:
        print("(the journal is empty)")


def restore(an, path, sha_prefix, to_stdout, list_only):
    st = an.store
    versions = [e for e in st.events() if e["path"] == path and e.get("sha")]
    if list_only or not sha_prefix:
        for e in versions:
            print(f"{time.strftime('%H:%M:%S', time.localtime(e['t']))}  {e['kind']:9s} {e['sha'][:8]}  {e.get('note') or ''}")
        if not versions:
            print(f"no recorded versions of {path}")
        return 0
    h, text = st.read_snapshot(sha_prefix)
    if to_stdout:
        sys.stdout.write(text)
        return 0
    is_lit = path in an.src.texts                      # the root or a chapter file
    target = os.path.join(an.src.base, path) if is_lit else os.path.join(an.outdir, path)
    if os.path.exists(target):
        st.log("backup", "lit" if is_lit else "gen", path, read_disk(target), note="before restore")
    os.makedirs(os.path.dirname(target) or ".", exist_ok=True)
    with open(target, "w", encoding="utf-8", newline="") as f:
        f.write(text)
    st.log("restore", "lit" if is_lit else "gen", path, text, note=f"restored {h[:8]}")
    print(f"restored {path} to {h[:8]}; it now counts as an edit, run `status` or `merge`")
    return 0


def adopt(source, outdir, base_lit, gen_is_base):
    store = Store(source, outdir)
    cur = sourcemap.read_source(source)
    if base_lit:
        old = Source.read(base_lit)
        store.set_base(tangle_text(old.text), old.text, note="adopt", parts=old.texts)
    elif gen_is_base:
        files = {n: (read_disk(os.path.join(outdir, n)) if os.path.exists(os.path.join(outdir, n)) else "")
                 for n in tangle_text(cur)}
        store.set_base(files, cur, note="adopt")
    else:
        raise MapError("say what the base is: --base-lit OLD.lit or --gen-is-base")
    print("base recorded; run `status`")
    return 0


def main():
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("-o", "--outdir", default=".", help="where the generated files and the .map live")
    ap = argparse.ArgumentParser(description="Reconcile a .lit with the files tangled from it, both ways.")
    ap.add_argument("source")
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("status", parents=[common]); p.add_argument("--check", action="store_true")
    p = sub.add_parser("diff", parents=[common]); p.add_argument("files", nargs="*")
    p = sub.add_parser("merge", parents=[common]); p.add_argument("--prefer", choices=["lit", "gen", "newer"])
    p.add_argument("--dry-run", action="store_true"); p.add_argument("--force", action="store_true")
    p = sub.add_parser("pick", parents=[common]); p.add_argument("files", nargs="*")
    p.add_argument("--force", action="store_true"); p.add_argument("--yes", action="store_true")
    p = sub.add_parser("log", parents=[common]); p.add_argument("-n", type=int, default=30); p.add_argument("--path")
    p = sub.add_parser("restore", parents=[common]); p.add_argument("path"); p.add_argument("--sha")
    p.add_argument("--list", action="store_true"); p.add_argument("--stdout", action="store_true")
    p = sub.add_parser("adopt", parents=[common]); p.add_argument("--base-lit"); p.add_argument("--gen-is-base", action="store_true")
    a = ap.parse_args()
    try:
        if a.cmd == "adopt":
            sys.exit(adopt(a.source, a.outdir, a.base_lit, a.gen_is_base))
        if a.cmd == "log":
            show_log(Store(a.source, a.outdir), a.n, a.path)
            return
        an = Analysis(a.source, a.outdir)
        if a.cmd == "status":
            sys.exit(status(an, a.check))
        if a.cmd == "restore":
            sys.exit(restore(an, a.path, a.sha, a.stdout, a.list))
        names = [f for f in (a.files if a.cmd in ("diff", "pick") and a.files else an.files)
                 if f in an.files and (an.files[f].regions)]
        if getattr(a, "files", None):
            bad = [f for f in a.files if f not in an.files]
            if bad:
                raise MapError(f"not generated from {a.source}: {', '.join(bad)}")
        if a.cmd == "diff":
            for n in names:
                for i in range(len(an.files[n].regions)):
                    show_region(an.files[n], i)
            if not names:
                print("both sides agree.")
        elif a.cmd == "merge":
            sys.exit(merge(an, a.prefer, a.dry_run, a.force))
        elif a.cmd == "pick":
            sys.exit(pick(an, names, a.yes, a.force))
    except MapError as e:
        sys.exit(f"sync: {e}")


if __name__ == "__main__":
    main()
