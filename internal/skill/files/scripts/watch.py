#!/usr/bin/env python3
"""Record every change to a .lit and to the files generated from it.  It never modifies any of them.

    python watch.py SOURCE.lit [-o OUTDIR] [--interval 0.5] [--settle 0.4] [--once] [--for SECONDS] [--quiet]

Leave it running in a terminal while you work.  Each time the .lit or a generated file changes (and has
stopped changing for --settle seconds, so half-saved files are not recorded) it saves the new version in
OUTDIR/SOURCE.sync/snapshots, appends an event to the journal, and prints one line, followed by a summary of
what is now waiting to be reconciled.  Nothing is written to your files and nothing is synced; you decide
when to run `sync.py merge` or `sync.py pick`.

Why record at all?  Because the order of tools no longer matters, and nothing is lost:
  * if tangle.py or sync.py later replaces an edited file, the edit is still there:
        python sync.py SOURCE.lit restore FILE --list
  * the journal gives each side's last-edit time, which `sync.py merge --prefer newer` uses for conflicts
  * `sync.py SOURCE.lit log` shows what happened, in order

Each event is classified:  edit    a person changed it
                           tangled a generated file now equals what the .lit produces
                           synced  the .lit is the version recorded at the last sync / tangle
--once records whatever changed since the watcher last ran (also while it was not running) and exits.
Polling is used so that nothing needs installing; the cost is a stat() per file every interval.
"""
import argparse
import difflib
import os
import sys
import time

import sourcemap
from absorb import read_disk, tangle_text
from litsync import Analysis, Store, sha
from sourcemap import MapError

COLOR = sys.stdout.isatty() and "NO_COLOR" not in os.environ
paint = lambda s, code: f"\033[{code}m{s}\033[0m" if COLOR else s
KIND_COLOR = {"edit": "33", "tangled": "32", "synced": "32", "deleted": "31"}


def delta(old, new):
    a, b = old.split("\n"), new.split("\n")
    plus = minus = 0
    for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, a, b, autojunk=False).get_opcodes():
        if tag in ("replace", "delete"):
            minus += i2 - i1
        if tag in ("replace", "insert"):
            plus += j2 - j1
    return plus, minus


class Watcher:
    def __init__(self, source, outdir, quiet=False, settle=0.4):
        self.source, self.outdir, self.quiet, self.settle = source, outdir, quiet, settle
        self.store = Store(source, outdir)
        os.makedirs(self.store.dir, exist_ok=True)
        self.stat, self.dirty, self._ours = {}, {}, (None, None)
        self.seen_path = self.store.p("watch.json")
        self.seen = self._load_seen()
        self.lit_name = os.path.basename(source)

    # ---- what is watched
    def targets(self):
        m = sourcemap.load_map(sourcemap.map_path(self.source, self.outdir))
        base = os.path.dirname(self.source)                  # the root file and every chapter it includes
        lits = [("lit", n, os.path.join(base, n)) for n in m.get("sources") or [self.lit_name]]
        return lits + [("gen", n, os.path.join(self.outdir, n)) for n in m["files"]]

    def _load_seen(self):
        import json
        try:
            with open(self.seen_path, encoding="utf-8") as f:
                return json.load(f)
        except (FileNotFoundError, ValueError):
            return {}

    def _save_seen(self):
        import json
        with open(self.seen_path, "w", encoding="utf-8") as f:
            json.dump(self.seen, f)

    # ---- classification
    def ours(self):
        try:
            text = sourcemap.read_source(self.source)
        except (OSError, MapError):
            return None
        if self._ours[0] != sha(text):
            try:
                self._ours = (sha(text), tangle_text(text))
            except (SystemExit, MapError, Exception):          # the .lit is mid-edit and does not parse
                self._ours = (sha(text), None)
        return self._ours[1]

    def classify(self, side, name, text):
        if text is None:
            return "deleted"
        if side == "lit":
            st = self.store.state()
            return "synced" if sha(text) == st.get("lit_files", {}).get(name, st.get("lit_sha")) else "edit"
        ours = self.ours()
        return "tangled" if ours is not None and ours.get(name) == text else "edit"

    def vs_base(self, side, name, text):
        if text is None:
            return ""
        base = self.store.base_lit(name) if side == "lit" else None
        if side == "gen":
            try:
                base = read_disk(self.store.p("base", name))
            except FileNotFoundError:
                base = None
        if base is None:
            return ""
        p, m = delta(base, text)
        return f"+{p} -{m} vs base" if (p or m) else "same as base"

    # ---- recording
    def say(self, line):
        if not self.quiet:
            print(line, flush=True)

    def record(self, side, name, path):
        key = f"{side}:{name}"
        text = read_disk(path) if os.path.exists(path) else None
        h = sha(text) if text is not None else "deleted"
        if self.seen.get(key) == h:
            return False
        prev = self.seen.get(key)
        kind = self.classify(side, name, text)
        ev = self.store.log(kind, side, name, text, prev=prev if prev and prev != "deleted" else None)
        self.seen[key] = h
        self._save_seen()
        when = time.strftime("%H:%M:%S", time.localtime(ev["t"]))
        snap = f"[snapshot {ev['sha'][:8]}]" if ev["sha"] else ""
        self.say(f"{when}  {side:3s}  {name:28s} {paint(f'{kind:8s}', KIND_COLOR.get(kind, '0'))} "
                 f"{self.vs_base(side, name, text):18s} {snap}")
        return True

    def summary(self):
        try:
            an = Analysis(self.source, self.outdir, self.store)
        except (MapError, SystemExit) as e:
            self.say(f"          (cannot compare the two sides right now: {e})")
            return
        n_lit = sum(1 for fs in an.files.values() for r in fs.regions if r.kind == "lit")
        n_gen = sum(1 for fs in an.files.values() for r in fs.regions if r.kind == "gen")
        n_con = sum(1 for fs in an.files.values() for r in fs.regions if r.kind == "conflict")
        if not (n_lit or n_gen or n_con):
            self.say("          both sides agree")
            return
        bits = [f"{n_lit} change(s) from the .lit" if n_lit else "", f"{n_gen} edit(s) in generated files" if n_gen else "",
                paint(f"{n_con} CONFLICT(s)", "31") if n_con else ""]
        self.say("          waiting: " + ", ".join(b for b in bits if b) + f"   ->  python sync.py {self.source} status")

    # ---- the loop
    def start(self):
        changed = False
        for side, name, path in self.targets():
            key = f"{side}:{name}"
            if key not in self.seen:                                    # first sight: a baseline, not an event
                self.seen[key] = sha(read_disk(path)) if os.path.exists(path) else "deleted"
            elif self.record(side, name, path):                         # changed while we were not watching
                changed = True
            self.stat[key] = self._stat(path)
        self._save_seen()
        self.store.log("watch", "lit", self.lit_name, note="started")
        if changed:
            self.summary()

    @staticmethod
    def _stat(path):
        try:
            st = os.stat(path)
            return (st.st_mtime_ns, st.st_size)
        except FileNotFoundError:
            return None

    def scan(self, now):
        recorded = False
        for side, name, path in self.targets():
            key = f"{side}:{name}"
            cur = self._stat(path)
            if cur != self.stat.get(key):
                self.stat[key] = cur
                self.dirty[key] = now                                   # still changing: wait for it to settle
            elif key in self.dirty and now - self.dirty[key] >= self.settle:
                del self.dirty[key]
                recorded |= self.record(side, name, path)
        if recorded:
            self.summary()

    def flush(self):
        for side, name, path in self.targets():
            self.dirty.pop(f"{side}:{name}", None)
            self.record(side, name, path)


def main():
    ap = argparse.ArgumentParser(description="Record changes to a .lit and its generated files (read-only).")
    ap.add_argument("source")
    ap.add_argument("-o", "--outdir", default=".")
    ap.add_argument("--interval", type=float, default=0.5)
    ap.add_argument("--settle", type=float, default=0.4)
    ap.add_argument("--once", action="store_true")
    ap.add_argument("--for", dest="duration", type=float, help="stop after this many seconds (useful in scripts)")
    ap.add_argument("--quiet", action="store_true")
    a = ap.parse_args()
    try:
        w = Watcher(a.source, a.outdir, a.quiet, a.settle)
        w.start()
        if a.once:
            w.flush()
            w.summary()
            return
        w.say(f"watching {a.source} and its generated files in {a.outdir} (Ctrl-C to stop); nothing is modified")
        end = time.time() + a.duration if a.duration else None
        while end is None or time.time() < end:
            w.scan(time.time())
            time.sleep(a.interval)
        w.flush()
    except MapError as e:
        sys.exit(f"watch: {e}")
    except KeyboardInterrupt:
        w.flush()
        print(f"\nstopped; {len(w.store.events())} events are in {w.store.dir}/journal.jsonl")


if __name__ == "__main__":
    main()
