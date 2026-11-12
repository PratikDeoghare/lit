#!/usr/bin/env python3
"""Two-way reconciliation of a .lit and the files tangled from it (library; see sync.py and watch.py).

The problem: edit the .lit and a generated file, and whichever of tangle.py / absorb.py runs first
discards the other edit.  The fix is a three-way merge against a remembered common base, done in the one
coordinate system both sides share, the generated files:

    base   the generated files at the last sync (written by tangle.py / absorb.py / sync.py)
    lit    tangle(current .lit)          what the .lit side now says
    gen    the files on disk             what you typed into the generated files

Where only one side changed a region, that change crosses over (a .lit change regenerates the file; a file
edit is folded into the .lit through the sourcemap, exactly as absorb.py does).  Where both changed the
same lines differently it is a conflict, and you choose.  Anything you choose to leave alone stays pending
on its side: the base keeps the old text for that region, so it is still a change next time.

State lives in <outdir>/<source>.sync/:
    base/<file>, base.lit, state.json    the common base (the .lit text is kept for reference)
    snapshots/<sha256>                   every version ever recorded, content addressed
    journal.jsonl                        events: edits seen by watch.py, backups, syncs, with timestamps
Nothing here ever deletes a recorded version.
"""
import difflib
import hashlib
import json
import os
import shutil
import sys
import tempfile
import time
from dataclasses import dataclass, field

import sourcemap
from absorb import Plan, diff_hunks, read_disk, tangle_text, to_lines
from sourcemap import MapError, file_body
from tangle import Source

APPLY, LEAVE, REVERT, TAKE_LIT, TAKE_GEN, BOTH, CUSTOM = "apply", "leave", "revert", "lit", "gen", "both", "custom"


def sha(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


# ================================================================ the store
class Store:
    def __init__(self, source, outdir):
        self.source, self.outdir = source, outdir
        self.dir = os.path.join(outdir, os.path.basename(source) + ".sync")

    def p(self, *parts):
        return os.path.join(self.dir, *parts)

    def has_base(self):
        return os.path.exists(self.p("state.json"))

    def state(self):
        try:
            with open(self.p("state.json"), encoding="utf-8") as f:
                return json.load(f)
        except FileNotFoundError:
            return {}

    def base_files(self):
        return {n: read_disk(self.p("base", n)) for n in self.state().get("files", [])}

    def base_lit(self, name=None):
        """The .lit text at the last sync: the whole program text, or one chapter file of it."""
        try:
            return read_disk(self.p("base_lit", name) if name else self.p("base.lit"))
        except FileNotFoundError:
            return None if name is None else self.base_lit()

    def set_base(self, files, lit_text, note="", parts=None):
        """Record the common base: generated `files`, the program text, and (for a program split into
        chapter files) each chapter `parts` {name: text}."""
        parts = parts or {os.path.basename(self.source): lit_text}
        shutil.rmtree(self.p("base"), ignore_errors=True)
        shutil.rmtree(self.p("base_lit"), ignore_errors=True)
        for name, text in parts.items():
            path = self.p("base_lit", name)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8", newline="") as f:
                f.write(text)
        for name, text in files.items():
            path = self.p("base", name)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as f:
                f.write(text)
        with open(self.p("base.lit"), "w", encoding="utf-8", newline="") as f:
            f.write(lit_text)
        with open(self.p("state.json"), "w", encoding="utf-8") as f:
            json.dump({"files": sorted(files), "lit_sha": sha(lit_text), "lit_files": {n: sha(t) for n, t in parts.items()},
                       "t": time.time(), "note": note}, f)
        self.snapshot(lit_text)
        for name, text in parts.items():
            self.log("base", "lit", name, text, note=note)

    def snapshot(self, text):
        h = sha(text)
        path = self.p("snapshots", h)
        if not os.path.exists(path):
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8", newline="") as f:
                f.write(text)
        return h

    def read_snapshot(self, prefix):
        hits = [h for h in os.listdir(self.p("snapshots"))] if os.path.isdir(self.p("snapshots")) else []
        hits = [h for h in hits if h.startswith(prefix)]
        if len(hits) != 1:
            raise MapError(f"snapshot {prefix!r} matches {len(hits)} recorded versions")
        return hits[0], read_disk(self.p("snapshots", hits[0]))

    def log(self, kind, side, path, text=None, **extra):
        os.makedirs(self.dir, exist_ok=True)
        ev = {"t": time.time(), "kind": kind, "side": side, "path": path,
              "sha": self.snapshot(text) if text is not None else None, **extra}
        with open(self.p("journal.jsonl"), "a", encoding="utf-8") as f:
            f.write(json.dumps(ev) + "\n")
        return ev

    def events(self):
        try:
            with open(self.p("journal.jsonl"), encoding="utf-8") as f:
                return [json.loads(l) for l in f if l.strip()]
        except FileNotFoundError:
            return []

    def last_edit(self, side, path):
        t = [e["t"] for e in self.events() if e["kind"] == "edit" and e["side"] == side and e["path"] == path]
        return max(t) if t else None


def protect(source, outdir, name, path, new_text):
    """Called by tangle.py before it overwrites a generated file: keep a copy of edits that are not in the .lit."""
    if not os.path.exists(path):
        return
    try:
        cur = read_disk(path)
    except (OSError, UnicodeDecodeError):
        return
    if cur == new_text:
        return
    store = Store(source, outdir)
    if store.has_base():
        known = store.base_files().get(name)
    else:
        try:
            known = None if sourcemap.load_map(sourcemap.map_path(source, outdir))["files"][name]["sha256"] != sha(cur) \
                else cur
        except (MapError, KeyError):
            known = None
    if known is not None and cur == known:
        return                                        # untouched since the last sync; nothing to protect
    h = store.log("backup", "gen", name, cur, note="replaced by tangle.py")["sha"]
    print(f"warning: {path} had edits that are not in the .lit; tangle replaced them, but a copy is kept "
          f"(snapshot {h[:8]}; see sync.py {source} log / restore)", file=sys.stderr)


# ================================================================ three-way regions
@dataclass
class Region:
    s: int                    # base lines [s, e) are affected
    e: int
    base: list
    lit: list                 # what the .lit side has there
    gen: list                 # what the generated file has there
    kind: str                 # "lit" | "gen" | "same" | "conflict"


def _hunks(old, new):
    if old == new:
        return []
    out = []
    for a, b, c, d, removed, added in diff_hunks(old, new):
        s = a if b == 0 else a - 1
        out.append((s, s + b, added))
    return out


def _overlap(g, h):
    if h[0] == h[1] == g["s"] == g["e"]:
        return True                                   # two insertions at the same point
    return h[0] < g["e"] and g["s"] < h[1]


def diff3(base, ours, theirs):
    """Regions where ours (the .lit side) or theirs (the file) differ from base, with who changed what."""
    items = sorted([(s, e, n, "o") for s, e, n in _hunks(base, ours)] +
                   [(s, e, n, "t") for s, e, n in _hunks(base, theirs)], key=lambda h: (h[0], h[1], h[3]))
    groups = []
    for it in items:
        if groups and _overlap(groups[-1], it):
            g = groups[-1]
            g["s"], g["e"] = min(g["s"], it[0]), max(g["e"], it[1])
            g["h"].append(it)
        else:
            groups.append({"s": it[0], "e": it[1], "h": [it]})
    regions = []
    for g in groups:
        s, e = g["s"], g["e"]

        def side(which):
            out, pos = [], s
            for hs, he, new, who in sorted(g["h"], key=lambda h: (h[0], h[1])):
                if who == which:
                    out += base[pos:hs] + new
                    pos = he
            return out + base[pos:e]

        has_o, has_t = any(h[3] == "o" for h in g["h"]), any(h[3] == "t" for h in g["h"])
        lit, gen = side("o"), side("t")
        kind = "lit" if not has_t else "gen" if not has_o else "same" if lit == gen else "conflict"
        regions.append(Region(s, e, base[s:e], lit, gen, kind))
    return regions


def default_decision(r):
    return (LEAVE, None) if r.kind == "conflict" else (APPLY, None)


def outcome(r, decision):
    """(what the .lit side will yield, what the file will hold, what the base becomes) for one region."""
    act, custom = decision
    if act == APPLY:
        if r.kind == "conflict":
            raise MapError("a conflict has to be resolved, not applied")
        x = r.lit if r.kind in ("lit", "same") else r.gen
        return x, x, x
    if act == REVERT:
        return r.base, r.base, r.base
    if act == LEAVE:
        return r.lit, r.gen, r.base
    if act == TAKE_LIT:
        return r.lit, r.lit, r.lit
    if act == TAKE_GEN:
        return r.gen, r.gen, r.gen
    if act == BOTH:
        both = r.lit + r.gen
        return both, both, both
    if act == CUSTOM:
        return custom, custom, custom
    raise MapError(f"unknown decision {act!r}")


# ================================================================ analysis of the current state
@dataclass
class FileState:
    name: str
    base: list
    ours: list
    gen: list
    missing: bool
    regions: list = field(default_factory=list)


class Analysis:
    def __init__(self, source, outdir=".", store=None):
        self.source, self.outdir = source, outdir
        self.store = store or Store(source, outdir)
        self.src = Source.read(source)
        self.lit_text = self.src.text
        self.ours = tangle_text(self.lit_text)
        self.base = self._base()
        self.files = {}
        for name in sorted(self.ours):
            path = os.path.join(outdir, name)
            missing = not os.path.exists(path)
            B = to_lines(self.base.get(name, ""))
            O = to_lines(self.ours[name])
            T = O if missing else to_lines(read_disk(path))
            fs = FileState(name, B, O, T, missing)
            fs.regions = diff3(B, O, T)
            self.files[name] = fs

    def _base(self):
        if self.store.has_base():
            return self.store.base_files()
        try:
            m = sourcemap.load_map(sourcemap.map_path(self.source, self.outdir))
        except MapError:
            raise MapError("no sync base yet: run tangle.py once (it records one)")
        if sourcemap.sha256(self.lit_text) == m["source_sha256"]:
            return dict(self.ours)                    # the .lit is unchanged since it was tangled
        raise MapError("no sync base was recorded and the .lit changed since the last tangle; give the .lit "
                       "the generated files came from: sync.py adopt --base-lit OLD.lit (or --gen-is-base "
                       "if you never edited the generated files)")

    def pending(self):
        return {n: fs.regions for n, fs in self.files.items() if fs.regions or fs.missing}

    def lit_changed(self):
        return sha(self.lit_text) != self.store.state().get("lit_sha")


# ================================================================ applying decisions
@dataclass
class Outcome:
    new_lit: str
    gen_text: dict            # file -> text to write
    base_text: dict           # file -> new base
    regenerated: dict
    ripple: dict              # file -> (wanted lines, lines the patched .lit really yields)
    notes: list
    errors: list
    lit_changed: bool


def prepare(an, decisions):
    """Compute the result of `decisions` ({(file, region index): (action, custom lines)}); writes nothing."""
    L, G, B = {}, {}, {}
    for name, fs in an.files.items():
        l, g, b, pos = [], [], [], 0
        for i, r in enumerate(fs.regions):
            lt, gt, bt = outcome(r, decisions.get((name, i)) or default_decision(r))
            stable = fs.base[pos:r.s]
            l += stable + lt
            g += stable + gt
            b += stable + bt
            pos = r.e
        stable = fs.base[pos:]
        L[name], G[name], B[name] = l + stable, g + stable, b + stable

    new_lit, notes, errors = an.lit_text, [], []
    need = [n for n, fs in an.files.items() if L[n] != fs.ours]
    if need:
        m, _ = sourcemap.build(an.source, src=an.src)
        plan = Plan(m, an.src)
        for n in need:
            plan.absorb_file(n, an.files[n].ours, L[n])
        errors, notes = list(plan.errors), list(plan.notes)
        if not errors:
            new_lit = plan.apply()
    regenerated, ripple = (tangle_text(new_lit) if not errors else dict(an.ours)), {}
    for n, fs in an.files.items():
        got = to_lines(regenerated[n])
        if not errors and got != L[n]:
            ripple[n] = (L[n], got)
            G[n], B[n] = got, got                     # the .lit wins in places we could not keep apart
    return Outcome(new_lit, {n: file_body(g) for n, g in G.items()}, {n: file_body(b) for n, b in B.items()},
                   regenerated, ripple, notes, errors, new_lit != an.lit_text)


def commit(an, out, note="sync"):
    """Write an Outcome: back up what is about to change, write the .lit and files, record the new base."""
    st = an.store
    name = os.path.basename(an.source)
    if out.lit_changed:
        for n in an.src.split_back(out.new_lit):             # only the chapter files that change
            st.log("backup", "lit", n, an.src.texts[n], note=f"before {note}")
        an.src.write_back(out.new_lit)
    written = []
    for n, text in out.gen_text.items():
        path = os.path.join(an.outdir, n)
        cur = None if an.files[n].missing else read_disk(path)
        if cur is not None and to_lines(cur) == to_lines(text):
            continue
        if cur is not None and to_lines(cur) != an.files[n].base:      # worth keeping: it was not just the old output
            st.log("backup", "gen", n, cur, note=f"before {note}")
        os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        written.append(n)
    sourcemap.write(an.source, an.outdir, base=False)
    st.set_base(out.base_text, out.new_lit, note=note, parts=an.src.texts)
    st.log(note, "lit", name, None, files=written)
    return written
