#!/usr/bin/env python3
"""Sourcemap between a brashtag literate source and the files tangle.py generates.

tangle.py writes <outdir>/<source>.map next to the generated files (use --no-map to skip it).
This module builds that map and answers lookups in both directions:

    python sourcemap.py to-lit  out/llm-rig.lit.map rig/model.py:45[:COL]   # generated -> .lit (file:line:col)
    python sourcemap.py to-gen  out/llm-rig.lit.map 182[:COL]                # .lit -> generated

How the map is made
  * brashtag's parser reports no positions, but its tree is lossless ('#tag{' + kids + '}'),
    so the .lit offset of every fenced block is found by summing lengths (and is asserted).
  * To learn where each generated line came from, every code line is tagged with an invisible
    marker and expanded with tangle.expand itself; the tag survives expansion, and whatever
    precedes the original text is the indentation contributed by <<<references>>>.
    The decoded result is checked, line by line, against tangle's own untagged expansion.

Map file (JSON, one block / one file per line)
  "source", "source_sha256"      the .lit it was built from (path is relative to the map); the hash
                                 is of the program text, with every #include already spliced in
  "sources"                      the files that text came from, the root first (chapter paths are
                                 relative to the root's directory); all offsets below are
                                 offsets into the spliced text, tangle.Source maps them to files
  "blocks": [{"chunk", "indent", "lines": [[start, end], ...], "nl_last"}]
        one entry per fenced code block: the chunk it belongs to, the indentation stripped from
        every line, character offsets [start, end) of each content line in the .lit (end is
        before the newline), and whether a newline follows the last content line
  "files": {name: {"sha256", "lines", "segments": [[at, n, block, line, prefix], ...]}}
        generated lines at..at+n-1 (1-based) come from lines line..line+n-1 of that block;
        prefix is the indentation added by references (the generated line is prefix + text)
"""
import bisect
import hashlib
import json
import os
import re
import sys

from tangle import Source, Bag, Blob, Code, FILE, RESERVED, REF, collect, expand, fence_text, is_fence, load, unescape

VERSION = 1
MARK = "\x00"
MARKED = re.compile("\x00(\\d+),(\\d+)\x00$")


class MapError(Exception):
    pass


def sha256(s):
    return hashlib.sha256(s.encode("utf-8")).hexdigest()


def read_source(path):
    """The .lit text exactly as the map's offsets see it: LF line endings only, with every
    `#include{chapter.lit}` replaced by that file's text (see tangle.Source)."""
    return Source.read(path).text


def file_body(lines):
    """What tangle writes for a file made of these lines."""
    body = "\n".join(lines)
    return body + "\n" if body else ""


# ---------------------------------------------------------------- positions in the .lit
def code_offsets(root, text):
    """id(Code) -> offset in `text` of the first character of that code's text."""
    pos, out = 0, {}

    def go(bag):
        nonlocal pos
        for k in bag.kids:
            if isinstance(k, Blob):
                pos += len(k.text)
            elif isinstance(k, Code):
                start = pos + len(k.tag)
                if text[start:start + len(k.text)] != k.text:
                    raise MapError("internal error: parse tree does not match the source text")
                out[id(k)] = start
                pos = start + len(k.text) + len(k.tag)
            else:
                pos += len(k.tag) + 2            # '#' tag '{'
                go(k)
                pos += 1                         # '}'

    go(root)
    if pos != len(text):
        raise MapError("internal error: offsets do not add up to the source length")
    return out


def layout(code, off):
    """Mirror of tangle.fence_text that also records where each line lives in the .lit.
    Returns (indent, [(text, start, end)], newline_follows_last_line)."""
    raw = code.text.split("\n")
    starts, pos = [], off
    for r in raw:
        starts.append(pos)
        pos += len(r) + 1
    lo, hi, indent = 0, len(raw), ""
    if raw[0].strip() == "":
        lo = 1
    if hi > lo and raw[hi - 1].strip() == "":
        indent, hi = raw[hi - 1], hi - 1
    items = []
    for i in range(lo, hi):
        r = raw[i]
        items.append((r[len(indent):] if r.startswith(indent) else r.lstrip(), starts[i], starts[i] + len(r)))
    if [t for t, _, _ in items] != fence_text(code):
        raise MapError("internal error: block layout differs from tangle.fence_text")
    return indent, items, hi < len(raw)


# ---------------------------------------------------------------- building the map
def build(src_path, chunks=None, src=None):
    """-> (map dict, {file name: generated text}).  `chunks` is tangle.collect's result if known,
    `src` the tangle.Source already read from src_path."""
    src = src or Source.read(src_path)
    text, root = src.text, src.parse()
    offs = code_offsets(root, text)
    blocks, texts, marked = [], [], {}

    def visit(bag):
        name = bag.tag.strip()
        if name in RESERVED:
            return
        out = marked.setdefault(name, [])
        for k in bag.kids:
            if isinstance(k, Code) and is_fence(k):
                indent, items, nl_last = layout(k, offs[id(k)])
                bid = len(blocks)
                blocks.append({"chunk": name, "indent": indent, "lines": [[s, e] for _, s, e in items],
                               "nl_last": nl_last})
                texts.append([t for t, _, _ in items])
                for li, (t, _, _) in enumerate(items):
                    out.append(t if REF.match(t) else f"{t}{MARK}{bid},{li}{MARK}")
            elif isinstance(k, Bag):
                visit(k)

    for k in root.kids:
        if isinstance(k, Bag):
            visit(k)
    if chunks is None:
        chunks = collect(root)

    files, generated = {}, {}
    for name in marked:
        if not FILE.search(name):
            continue
        plain = expand(name, chunks)
        segs = []
        for i, line in enumerate(expand(name, marked), 1):
            m = MARKED.search(line)
            body = line[:m.start()]
            bid, li = int(m.group(1)), int(m.group(2))
            t = texts[bid][li]
            if not body.endswith(t) or ("" if t == "" else unescape(body)) != plain[i - 1]:
                raise MapError(f"internal error: sourcemap for {name} disagrees with tangle at line {i}")
            pfx = body[:len(body) - len(t)]
            if segs and segs[-1][2] == bid and segs[-1][3] + segs[-1][1] == li and segs[-1][4] == pfx \
                    and segs[-1][0] + segs[-1][1] == i:
                segs[-1][1] += 1
            else:
                segs.append([i, 1, bid, li, pfx])
        if len(plain) != sum(s[1] for s in segs):
            raise MapError(f"internal error: sourcemap for {name} has the wrong number of lines")
        content = file_body(plain)
        files[name] = {"sha256": sha256(content), "lines": len(plain), "segments": segs}
        generated[name] = content
    return {"version": VERSION, "source_sha256": sha256(text), "sources": src.order, "blocks": blocks,
            "files": files}, generated


def dumps(m, source):
    j = lambda x: json.dumps(x, separators=(",", ":"))
    return "\n".join([
        "{", f'"version":{m["version"]},', f'"source":{j(source)},', f'"sources":{j(m.get("sources", []))},', f'"source_sha256":{j(m["source_sha256"])},',
        '"blocks":[', ",\n".join(j(b) for b in m["blocks"]), "],",
        '"files":{', ",\n".join(f"{j(k)}:{j(v)}" for k, v in m["files"].items()), "}", "}"]) + "\n"


def map_path(src, outdir):
    return os.path.join(outdir, os.path.basename(src) + ".map")


def write(src, outdir, chunks=None, base=True):
    """Write the map.  With base=True (what tangle.py and absorb.py want) also record the generated
    files as the common base for two-way sync (litsync.py)."""
    so = Source.read(src)
    m, generated = build(src, chunks, so)
    path = map_path(src, outdir)
    os.makedirs(outdir or ".", exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(dumps(m, os.path.relpath(os.path.abspath(src), os.path.dirname(os.path.abspath(path)))))
    if base:
        try:
            import litsync
            litsync.Store(src, outdir).set_base(generated, so.text, note="tangle", parts=so.texts)
        except ImportError:
            pass
    return path


def load_map(path):
    try:
        with open(path, encoding="utf-8") as f:
            m = json.load(f)
    except FileNotFoundError:
        raise MapError(f"{path}: no sourcemap; run tangle.py first")
    if m.get("version") != VERSION:
        raise MapError(f"{path}: unsupported sourcemap version {m.get('version')}")
    return m


def origins(entry):
    """One (block, line, prefix) per generated line of a file."""
    out = []
    for at, n, bid, li, pfx in entry["segments"]:
        out += [(bid, li + k, pfx) for k in range(n)]
    return out


# ---------------------------------------------------------------- lookups
def newline_index(text):
    return [i for i, c in enumerate(text) if c == "\n"]


def line_col(nl, off):
    ln = bisect.bisect_left(nl, off)
    return ln + 1, off - (nl[ln - 1] + 1 if ln else 0) + 1


def to_lit(m, fname, line, col=1):
    """Generated (file, line, col) -> (.lit offset, chunk name)."""
    if fname not in m["files"]:
        raise MapError(f"{fname} is not in the sourcemap")
    org = origins(m["files"][fname])
    if not 1 <= line <= len(org):
        raise MapError(f"{fname} has {len(org)} lines")
    bid, li, pfx = org[line - 1]
    blk = m["blocks"][bid]
    start, end = blk["lines"][li]
    off = start + len(blk["indent"]) + max(0, col - 1 - len(pfx))
    return min(off, end), blk["chunk"]


def to_gen(m, lit_line, text):
    """.lit line number -> [(file, line)] where that code line is generated."""
    nl = newline_index(text)
    lo = (nl[lit_line - 2] + 1) if lit_line >= 2 else 0
    hi = nl[lit_line - 1] if lit_line - 1 < len(nl) else len(text)
    hits = []
    for bid, blk in enumerate(m["blocks"]):
        for li, (s, e) in enumerate(blk["lines"]):
            if s <= hi and e >= lo and (s < e or lo <= s <= hi):
                for fname, entry in m["files"].items():
                    for at, n, b, first, _ in entry["segments"]:
                        if b == bid and first <= li < first + n:
                            hits.append((fname, at + li - first))
    return hits


def _cli(argv):
    if len(argv) < 3 or argv[0] not in ("to-lit", "to-gen", "build"):
        sys.exit(__doc__)
    if argv[0] == "build":
        print("wrote", write(argv[1], argv[2] if len(argv) > 2 else "."))
        return
    mp = load_map(argv[1])
    src = os.path.join(os.path.dirname(os.path.abspath(argv[1])), mp["source"])
    srcobj = Source.read(src)
    text = srcobj.text
    if argv[0] == "to-lit":
        parts = argv[2].split(":")
        fname, line = parts[0], int(parts[1])
        off, chunk = to_lit(mp, fname, line, int(parts[2]) if len(parts) > 2 else 1)
        name, ln, col = srcobj.locate(off)
        print(f"{name}:{ln}:{col}  chunk \u00ab{chunk}\u00bb")
    else:
        parts = argv[2].split(":")
        if len(parts) > 1 and parts[0] in srcobj.texts:        # chapter.lit:LINE
            at = srcobj.flat_offset(parts[0], int(parts[1]))
            if at is None:
                sys.exit(f"{argv[2]} is not part of the program text")
            line = newline_index(text) and line_col(newline_index(text), at)[0]
        else:
            line = int(parts[0])
        hits = to_gen(mp, line, text)
        print("\n".join(f"{f}:{n}" for f, n in hits) or "no generated line (not code, or the chunk is unused)")
    if sha256(text) != mp["source_sha256"]:
        print("warning: the .lit has changed since the map was written; results may be off", file=sys.stderr)


if __name__ == "__main__":
    try:
        _cli(sys.argv[1:])
    except MapError as e:
        sys.exit(str(e))
