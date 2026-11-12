#!/usr/bin/env python3
"""Translate addresses between a literate source and the files tangle.py generated from it.

    python addr.py SOURCE.lit ADDRESS [ADDRESS ...] [-o OUTDIR] [--json] [--map PATH] [--ignore-stale]

An ADDRESS is FILE:LINE[:COL] (1-based; columns count characters).  The direction is detected:

    python addr.py llm-rig.lit rig/model.py:64:13      generated -> .lit :  llm-rig.lit:158:9   <<attention init>>
    python addr.py llm-rig.lit llm-rig.lit:158:9       .lit -> generated :  rig/model.py:64:13
    python addr.py llm-rig.lit 158                     a bare LINE[:COL] is a .lit address

  * If the program is split into chapters with #include{ch.lit}, FILE may be the root or any chapter
    file (a bare LINE means a line of the root) and answers name the chapter file and its own line.
  * If FILE is the source, the answer lists every generated place that code is written to; a chunk
    expanded in several places gives several answers.  A <<<reference>>> line gives the places where
    the chunk it expands appears (this includes lines of nested chunks that are also expanded
    elsewhere); a prose line has no generated address.
  * If FILE is a generated file, the answer is the .lit line that produced it.  A column that falls
    inside indentation contributed by a <<<reference>>> maps to the start of the code in the .lit.
  * Generated files may have been edited since tangling.  Lines are then matched against the pristine
    output (difflib), so addresses refer to the file as it is now; a line you added or changed has no
    origin in the .lit yet (absorb.py would put it there).
  * Output is one `path:line:col` per answer (editor/grep friendly) with the chunk name after two
    spaces; --json gives a machine-readable list.  Exit status: 0 found, 1 no address, 2 error.

Needs the sourcemap that tangle.py writes (OUTDIR/<source>.map, default OUTDIR is .) and refuses to
guess if the .lit changed since (use --ignore-stale to look anyway; results may then be wrong).
"""
import argparse
import difflib
import json
import os
import sys

import sourcemap
from absorb import read_disk, tangle_text, to_lines
from sourcemap import MapError
from tangle import REF, Source


def parse_address(s):
    """'file:12:3' -> (file, 12, 3); 'file:12' -> (file, 12, None); '12' / '12:3' -> (None, 12, 3|None)."""
    parts = s.split(":")
    if len(parts) >= 3 and parts[-1].isdigit() and parts[-2].isdigit():
        return ":".join(parts[:-2]), int(parts[-2]), int(parts[-1])
    if len(parts) == 2 and parts[0].isdigit() and parts[1].isdigit():
        return None, int(parts[0]), int(parts[1])
    if len(parts) >= 2 and parts[-1].isdigit():
        return ":".join(parts[:-1]), int(parts[-1]), None
    if len(parts) == 1 and parts[0].isdigit():
        return None, int(parts[0]), None
    raise MapError(f"cannot read address {s!r}; expected FILE:LINE[:COL] or LINE[:COL]")


def align(pristine, current):
    """Line-number maps for the lines two versions of a file share: (current -> pristine, pristine -> current)."""
    c2p, p2c = {}, {}
    for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, pristine, current, autojunk=False).get_opcodes():
        if tag == "equal":
            for k in range(i2 - i1):
                p2c[i1 + k + 1], c2p[j1 + k + 1] = j1 + k + 1, i1 + k + 1
    return c2p, p2c


def ranges(numbers):
    """[3,4,5,9] -> ['3-5', '9']"""
    out, run = [], []
    for n in sorted(set(numbers)):
        if run and n == run[-1] + 1:
            run.append(n)
        else:
            if run:
                out.append(run)
            run = [n]
    if run:
        out.append(run)
    return [f"{r[0]}-{r[-1]}" if len(r) > 1 else str(r[0]) for r in out]


class Mapper:
    def __init__(self, source, outdir=".", map_file=None, ignore_stale=False):
        self.source, self.outdir = source, outdir
        self.src = Source.read(source)
        self.text = self.src.text
        self.m = sourcemap.load_map(map_file or sourcemap.map_path(source, outdir))
        self.stale = sourcemap.sha256(self.text) != self.m["source_sha256"]
        if self.stale and not ignore_stale:
            raise MapError(f"{source} has changed since it was tangled, so the sourcemap is out of date; "
                           "run tangle.py again (or pass --ignore-stale to look anyway)")
        self.nl = sourcemap.newline_index(self.text)
        self.org = {f: sourcemap.origins(e) for f, e in self.m["files"].items()}
        self.notes, self._align, self._pristine = [], {}, None

    # ---- naming
    def gen_path(self, fname):
        return os.path.normpath(os.path.join(self.outdir, fname))

    def source_name(self, name):
        """The name of the source file (the root or a chapter) an address means, or None."""
        for cand in (name, os.path.relpath(os.path.abspath(name), os.path.abspath(self.src.base or "."))):
            cand = os.path.normpath(cand).replace(os.sep, "/")
            if cand in self.src.texts:
                return cand
        return None

    def is_source(self, name):
        return self.source_name(name) is not None

    def source_path(self, name):
        return os.path.normpath(os.path.join(self.src.base, name))

    def flat_line(self, name, line):
        """Line `line` of source file `name` -> its line in the program text the map was built from."""
        total = self.src.texts[name].count("\n") + 1
        if not 1 <= line <= total:
            raise MapError(f"{name} has {total} lines, not {line}")
        off = self.src.flat_offset(name, line)
        if off is None:
            raise MapError(f"{name}:{line} is an #include line (or lies outside the program text); "
                           "address a line of the chapter itself")
        return sourcemap.line_col(self.nl, off)[0]

    def generated_name(self, name):
        if name in self.m["files"]:
            return name
        rel = os.path.relpath(name, self.outdir).replace(os.sep, "/")
        if rel in self.m["files"]:
            return rel
        raise MapError(f"{name} is neither {self.source} nor a file generated from it "
                       f"({', '.join(self.m['files'])})")

    # ---- generated files edited since tangling
    def alignment(self, fname):
        if fname not in self._align:
            res, path = None, os.path.join(self.outdir, fname)
            if not self.stale and os.path.exists(path):
                cur = read_disk(path)
                if sourcemap.sha256(cur) != self.m["files"][fname]["sha256"]:
                    if self._pristine is None:
                        self._pristine = tangle_text(self.text)
                    res = align(to_lines(self._pristine[fname]), to_lines(cur))
                    self.notes.append(f"{self.gen_path(fname)} was edited after tangling; "
                                      "its line numbers are those of the current file")
            self._align[fname] = res
        return self._align[fname]

    # ---- generated -> .lit
    def to_lit(self, fname, line, col=None):
        al = self.alignment(fname)
        pl = al[0].get(line) if al else line
        if pl is None:
            return [{"status": "edited", "msg": f"{self.gen_path(fname)}:{line} was added or changed after "
                     "tangling, so it has no origin in the .lit yet (absorb.py folds it in)"}]
        org = self.org[fname]
        if not 1 <= pl <= len(org):
            raise MapError(f"{self.gen_path(fname)} has {len(org)} lines, not {line}")
        bid, li, pfx = org[pl - 1]
        blk = self.m["blocks"][bid]
        start, end = blk["lines"][li]
        indent, note = len(blk["indent"]), None
        if col is None:
            lit_col = indent + 1
        elif col <= len(pfx):
            lit_col, note = indent + 1, "column lies in indentation added by a <<<reference>>>"
        else:
            lit_col = indent + col - len(pfx)
        off = start + min(lit_col - 1, end - start)
        name, ln, c = self.src.locate(off)
        return [{"file": self.source_path(name), "line": ln, "col": c, "chunk": blk["chunk"], **({"note": note} if note else {})}]

    # ---- .lit -> generated
    def lit_line_span(self, line):
        total = len(self.nl) + (0 if self.text.endswith("\n") else 1)
        if not 1 <= line <= max(total, 1):
            raise MapError(f"{self.source} has {total} lines, not {line}")
        lo = self.nl[line - 2] + 1 if line >= 2 else 0
        hi = self.nl[line - 1] if line - 1 < len(self.nl) else len(self.text)
        return lo, hi

    def gen_places(self, bids_lines):
        """{(block, line)} -> [(file, generated line, prefix)] across all generated files."""
        want, hits = set(bids_lines), []
        for fname, entry in self.m["files"].items():
            for at, n, b, first, pfx in entry["segments"]:
                for k in range(n):
                    if (b, first + k) in want:
                        hits.append((fname, at + k, pfx))
        return hits

    def current_line(self, fname, pristine_line):
        al = self.alignment(fname)
        return pristine_line if not al else al[1].get(pristine_line)

    def to_gen(self, line, col=None):
        lo, hi = self.lit_line_span(line)
        here = [(bid, li) for bid, blk in enumerate(self.m["blocks"]) for li, (s, e) in enumerate(blk["lines"])
                if lo <= s and e <= hi]
        if not here:
            return []
        results = []
        hits = self.gen_places(here)
        if hits:
            blk = self.m["blocks"][here[0][0]]
            for fname, gl, pfx in hits:
                cl = self.current_line(fname, gl)
                if cl is None:
                    results.append({"status": "edited", "msg": f"{self.gen_path(fname)}: the line generated here "
                                    "was changed after tangling"})
                    continue
                gcol = len(pfx) + 1 if col is None else len(pfx) + max(1, col - len(blk["indent"]))
                results.append({"file": self.gen_path(fname), "line": cl, "col": gcol, "chunk": blk["chunk"]})
            return results
        ref = REF.match(self.text[lo:hi])           # a <<<reference>>> line: where does the chunk appear?
        if ref:
            return self.reference_places(ref.group(2).strip())
        return []

    def reference_places(self, name):
        """Generated lines of chunk `name` and of every chunk it expands (found through the .lit's own lines)."""
        reach, todo = set(), [name]
        while todo:
            n = todo.pop()
            if n in reach:
                continue
            reach.add(n)
            for blk in self.m["blocks"]:
                if blk["chunk"] == n:
                    for s, e in blk["lines"]:
                        r = REF.match(self.text[s:e])
                        if r:
                            todo.append(r.group(2).strip())
        pairs = [(bid, li) for bid, blk in enumerate(self.m["blocks"]) if blk["chunk"] in reach
                 for li in range(len(blk["lines"]))]
        per_file = {}
        for fname, gl, _ in self.gen_places(pairs):
            cl = self.current_line(fname, gl)
            if cl is not None:
                per_file.setdefault(fname, []).append(cl)
        return [{"file": self.gen_path(f), "lines": ranges(ls), "chunk": name, "reference": True}
                for f, ls in per_file.items()]


def render(res, direction):
    if "status" in res:
        return f"# {res['msg']}"
    if res.get("reference"):
        return f"{res['file']}:{','.join(res['lines'])}  (where \u00ab{res['chunk']}\u00bb is expanded)"
    s = f"{res['file']}:{res['line']}:{res['col']}  \u00ab{res['chunk']}\u00bb"
    return s + (f"  ({res['note']})" if res.get("note") else "")


def main():
    ap = argparse.ArgumentParser(description="Translate addresses between a .lit file and its tangled output.")
    ap.add_argument("source")
    ap.add_argument("addresses", nargs="+")
    ap.add_argument("-o", "--outdir", default=".")
    ap.add_argument("--map")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--ignore-stale", action="store_true")
    a = ap.parse_args()
    try:
        mp = Mapper(a.source, a.outdir, a.map, a.ignore_stale)
        report, found = [], True
        for addr in a.addresses:
            fname, line, col = parse_address(addr)
            if fname is None or mp.is_source(fname):
                name = mp.source_name(fname) if fname else mp.src.rel(mp.src.root)
                direction, res = "lit->gen", mp.to_gen(mp.flat_line(name, line), col)
            else:
                g = mp.generated_name(fname)
                direction, res = "gen->lit", mp.to_lit(g, line, col)
            found &= any("status" not in r for r in res)
            report.append({"address": addr, "direction": direction, "results": res})
    except MapError as e:
        sys.exit(f"addr: {e}")
    if a.json:
        print(json.dumps(report, indent=1, ensure_ascii=False))
    else:
        many = len(report) > 1
        for r in report:
            if many:
                print(f"{r['address']}  [{r['direction']}]")
            for res in r["results"]:
                print(("  " if many else "") + render(res, r["direction"]))
            if not r["results"]:
                print(("  " if many else "") + "# no address: not code, or the chunk is never expanded into a file")
    for n in mp.notes:
        print("note: " + n, file=sys.stderr)
    sys.exit(0 if found else 1)


if __name__ == "__main__":
    main()
