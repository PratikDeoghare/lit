#!/usr/bin/env python3
"""Fold edits made directly to tangled files back into the literate source.

    python absorb.py llm-rig.lit [FILE ...] [-o OUTDIR] [--dry-run] [--force] [--map PATH]

  FILE ...     the generated files you edited (default: every file in the sourcemap that differs)
  -o OUTDIR    where tangle.py wrote the files and <source>.map (default: .)
  --dry-run    show what would change in the .lit, write nothing
  --force      apply even if the patched .lit would regenerate something other than what is on disk
               (see "side effects" below); the generated files are then overwritten to match the .lit

How it works
  1. Check that the .lit is unchanged since the sourcemap was made, and re-tangle it in memory to
     get the pristine version of each generated file.
  2. `git diff --no-index -U0` between pristine and edited file gives the hunks (Python's difflib
     is used instead if git is missing).  Each changed or deleted line is mapped through the
     sourcemap to the .lit line that produced it; the indentation that <<<references>>> add is
     removed and the block's own indentation put back.  Inserted lines go after the preceding
     line's origin (before the first line, if inserted at the top); a note is printed when that
     choice is ambiguous.
  3. The patched .lit is tangled in memory again and compared with the files on disk.  If every
     file matches exactly, the .lit is written, and the generated files and sourcemap refreshed.

Side effects: a chunk that is expanded in several places, or edits that cannot be expressed in the
.lit (an indentation shallower than a reference's), make the re-tangled output differ from what you
have on disk.  absorb.py reports those differences and refuses unless you pass --force.

Edit only code.  Prose, headings and chunk structure live in the .lit; change them there.
"""
import argparse
import difflib
import os
import re
import subprocess
import sys
import tempfile

import sourcemap
from sourcemap import MapError
from tangle import FILE, Source, collect, expand, parse_flat

HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")


def to_lines(text):
    lines = text.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    return lines


def read_disk(path):
    with open(path, encoding="utf-8") as f:          # universal newlines: CRLF edits are fine
        return f.read()


def tangle_text(lit_text):
    """{file: generated text} for program text given as a string, using tangle's own collect/expand."""
    chunks = collect(parse_flat(lit_text))
    return {n: sourcemap.file_body(expand(n, chunks)) for n in chunks if FILE.search(n)}


def diff_hunks(old, new):
    """git's -U0 hunks as (a, b, c, d, removed, added): old lines a..a+b-1 became new lines c..c+d-1
    (for b == 0, the lines were inserted after old line a)."""
    out, ok = "", False
    with tempfile.TemporaryDirectory() as d:
        po, pn = os.path.join(d, "old"), os.path.join(d, "new")
        for p, ls in ((po, old), (pn, new)):
            with open(p, "w", encoding="utf-8", newline="") as f:
                f.write("".join(l + "\n" for l in ls))
        try:
            r = subprocess.run(["git", "diff", "--no-index", "--no-color", "--no-ext-diff", "-U0",
                                "--diff-algorithm=histogram", po, pn], capture_output=True, encoding="utf-8")
            out, ok = r.stdout, r.returncode in (0, 1)
        except FileNotFoundError:
            pass
    if not ok:                                         # same hunk convention as git
        out = "\n".join(difflib.unified_diff(old, new, n=0, lineterm=""))
    hunks, cur = [], None
    for ln in out.split("\n"):
        m = HUNK.match(ln)
        if m:
            cur = [int(m[1]), 1 if m[2] is None else int(m[2]), int(m[3]), 1 if m[4] is None else int(m[4]), [], []]
            hunks.append(cur)
        elif cur is not None and ln[:1] == "-":
            cur[4].append(ln[1:])
        elif cur is not None and ln[:1] == "+":
            cur[5].append(ln[1:])
    for h in hunks:
        if len(h[4]) != h[1] or len(h[5]) != h[3]:
            raise MapError("could not understand the diff output")
    return [tuple(h) for h in hunks]


class Plan:
    """Edits to the .lit, keyed by (block, line) so that a chunk expanded twice is edited once."""

    def __init__(self, m, src):
        self.m, self.src, self.text = m, src, src.text
        self.org = {f: sourcemap.origins(e) for f, e in m["files"].items()}
        self.lines, self.ins = {}, {}              # (block, line) -> text | None ; (block, line, where) -> [text]
        self.errors, self.notes, self.log = [], [], []

    def where(self, bid, li):
        blk = self.m["blocks"][bid]
        return f"\u00ab{blk['chunk']}\u00bb, {self.src.where(blk['lines'][li][0])}"

    def lit_text(self, bid, text, pfx, fname, lineno):
        if text == "":
            return ""
        if not text.startswith(pfx):
            self.errors.append(f"{fname}:{lineno}: indented less than the {len(pfx)} column(s) that "
                               f"<<<references>>> add at this place; change the reference in the .lit instead")
            return None
        return self.m["blocks"][bid]["indent"] + text[len(pfx):]

    def set_line(self, key, val, fname, lineno):
        if key in self.lines and self.lines[key] != val:
            self.errors.append(f"{fname}:{lineno}: edits the .lit line at {self.where(*key)} differently from "
                               "another copy of the same chunk; make the copies agree")
        else:
            self.lines[key] = val

    def replace(self, fname, lineno, new):
        bid, li, pfx = self.org[fname][lineno - 1]
        txt = self.lit_text(bid, new, pfx, fname, lineno)
        if txt is not None:
            self.set_line((bid, li), txt, fname, lineno)

    def delete(self, fname, lineno):
        bid, li, _ = self.org[fname][lineno - 1]
        self.set_line((bid, li), None, fname, lineno)

    def insert(self, fname, a, new):
        org = self.org[fname]
        if not org:
            self.errors.append(f"{fname}: no line of this file comes from the .lit, so there is nowhere to "
                               "anchor an insertion; write the code into the .lit by hand")
            return
        if a >= 1:
            bid, li, pfx = org[a - 1]
            where = "after"
            if a < len(org) and not (org[a][0] == bid and org[a][1] == li + 1):
                self.notes.append(f"{fname}: lines inserted after line {a} sit at a chunk boundary; placed at the end "
                                  f"of {self.where(bid, li)} (they could also start {self.where(org[a][0], org[a][1])})")
        else:
            bid, li, pfx = org[0]
            where = "before"
        lines = [self.lit_text(bid, t, pfx, fname, a + 1 + i) for i, t in enumerate(new)]
        if any(t is None for t in lines):
            return
        key = (bid, li, where)
        if key in self.ins and self.ins[key] != lines:
            self.errors.append(f"{fname}: inserts different text than another copy of the same chunk "
                               f"at {self.where(bid, li)}")
        else:
            self.ins[key] = lines

    def absorb_file(self, fname, old, new):
        for a, b, c, d, removed, added in diff_hunks(old, new):
            if b == 0:
                self.insert(fname, a, added)
                self.log.append(f"{fname}:{a}+  {d} line(s) added")
                continue
            for i in range(min(b, d)):
                self.replace(fname, a + i, added[i])
            if d > b:
                self.insert(fname, a + b - 1, added[b:])
            for i in range(d, b):
                self.delete(fname, a + i)
            bid, li, _ = self.org[fname][a - 1]
            rng = f"{a}-{a + b - 1}" if b > 1 else f"{a}"
            self.log.append(f"{fname}:{rng}  {self.where(bid, li)}: {min(b, d)} changed"
                            + (f", {d - b} added" if d > b else "") + (f", {b - d} removed" if d < b else ""))

    def apply(self):
        blocks, ops = self.m["blocks"], []             # (start, end, text, order)
        for (bid, li), new in self.lines.items():
            blk = blocks[bid]
            start, end = blk["lines"][li]
            if new is None:
                nl = li < len(blk["lines"]) - 1 or blk["nl_last"]
                ops.append((start, end + (1 if nl else 0), "", 2))
            else:
                ops.append((start, end, new, 2))
        for (bid, li, where), lines in self.ins.items():
            blk = blocks[bid]
            start, end = blk["lines"][li]
            if where == "before":
                ops.append((start, start, "\n".join(lines) + "\n", 1))
            elif li < len(blk["lines"]) - 1 or blk["nl_last"]:
                ops.append((end + 1, end + 1, "\n".join(lines) + "\n", 0))
            else:
                ops.append((end, end, "\n" + "\n".join(lines), 0))
        ops.sort(key=lambda o: (o[0], o[1], o[3]))
        out, cur = [], 0
        for start, end, new, _ in ops:
            if start < cur:
                raise MapError("overlapping edits; edit fewer lines at once")
            out += [self.text[cur:start], new]
            cur = end
        out.append(self.text[cur:])
        return "".join(out)


def short_diff(actual, expected, limit=14):
    d = list(difflib.unified_diff(actual, expected, "on disk", "regenerated from the patched .lit", n=0, lineterm=""))
    return d[:limit] + ([f"... ({len(d) - limit} more lines)"] if len(d) > limit else [])


def run(a):
    src = Source.read(a.source)
    text = src.text
    m = sourcemap.load_map(a.map or sourcemap.map_path(a.source, a.outdir))
    if sourcemap.sha256(text) != m["source_sha256"]:
        raise MapError(f"{a.source} has changed since it was tangled, so the sourcemap is stale. Tangle again "
                       "(this discards edits to generated files), absorb before touching the .lit, or merge both sides "
                       "with sync.py.")
    pristine = tangle_text(text)
    for f, e in m["files"].items():
        if sourcemap.sha256(pristine.get(f, "")) != e["sha256"]:
            raise MapError("the sourcemap does not match what this tangle.py generates; tangle again")

    targets = []
    for f in a.files or list(m["files"]):
        rel = f
        if a.files:
            rel = os.path.relpath(f, a.outdir) if os.path.exists(f) else f
            if rel not in m["files"]:
                raise MapError(f"{f} is not generated from {a.source} (known: {', '.join(m['files'])})")
        targets.append(rel)

    plan = Plan(m, src)
    touched = []
    for f in targets:
        path = os.path.join(a.outdir, f)
        if not os.path.exists(path):
            continue
        old, new = to_lines(pristine[f]), to_lines(read_disk(path))
        if old != new:
            touched.append(f)
            plan.absorb_file(f, old, new)
    if not touched:
        print("nothing to absorb: no generated file differs from what the .lit produces")
        return 0

    for line in plan.log:
        print("  " + line)
    for n in plan.notes:
        print("note: " + n)
    if plan.errors:
        print("cannot absorb:\n  " + "\n  ".join(plan.errors))
        return 1

    new_text = plan.apply()
    try:
        regenerated = tangle_text(new_text)
    except SystemExit as e:
        raise MapError(f"the patched .lit no longer parses ({e}); an edited line probably contains the "
                       "code fence delimiter")
    problems, affected = {}, set(touched)
    for f in m["files"]:
        path = os.path.join(a.outdir, f)
        if f not in regenerated:
            problems[f] = ["no longer generated by the patched .lit"]
        elif f in touched or to_lines(regenerated[f]) != to_lines(pristine[f]):
            affected.add(f)                            # edited, or changed by an edit to a shared chunk
            if os.path.exists(path) and to_lines(regenerated[f]) != to_lines(read_disk(path)):
                problems[f] = short_diff(to_lines(read_disk(path)), to_lines(regenerated[f]))
                if f not in touched and to_lines(read_disk(path)) != to_lines(pristine[f]):
                    problems[f].append("(this file also has edits you did not name; they would be overwritten)")
        # files the patch does not affect are left alone, even if they carry edits that are still to be absorbed

    changed = src.split_back(new_text)
    for name in sorted(changed):
        print("\n".join(difflib.unified_diff(src.texts[name].split("\n"), changed[name].split("\n"),
                                             f"a/{name}", f"b/{name}", n=1, lineterm="")))
    if problems:
        print("\nside effects: after absorbing, the .lit would regenerate these files differently from what is on disk")
        for f, d in problems.items():
            print(f"  {f}:\n    " + "\n    ".join(d))
        if not a.force:
            print("\nnothing written. Usually a chunk is used in several places: make the same edit everywhere "
                  "(or name the other files too), or pass --force to accept the .lit's version.")
            return 1
    if a.dry_run:
        print("\n(dry run: nothing written)")
        return 0

    src.write_back(new_text)
    for f in sorted(affected & set(regenerated)):
        path, content = os.path.join(a.outdir, f), regenerated[f]
        if not os.path.exists(path) or to_lines(read_disk(path)) != to_lines(content):
            with open(path, "w", encoding="utf-8") as fh:
                fh.write(content)
    sourcemap.write(a.source, a.outdir)
    print(f"\nabsorbed edits from {len(touched)} file(s) into {a.source}; sourcemap refreshed"
          + (f"; regenerated: {', '.join(problems)}" if problems else ""))
    return 0


def main():
    ap = argparse.ArgumentParser(description="Fold edits made to tangled files back into the .lit.")
    ap.add_argument("source")
    ap.add_argument("files", nargs="*")
    ap.add_argument("-o", "--outdir", default=".")
    ap.add_argument("--map")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--force", action="store_true")
    try:
        sys.exit(run(ap.parse_args()))
    except MapError as e:
        sys.exit(f"absorb: {e}")


if __name__ == "__main__":
    main()
