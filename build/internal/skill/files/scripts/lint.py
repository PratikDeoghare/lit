#!/usr/bin/env python3
"""Pre-flight checks for a brashtag literate source.

    python lint.py program.lit [--refs]

Errors (exit status 1): the .lit does not parse (stray '#' or '}' in prose, unterminated fence,
...), a <<<reference>>> to an undefined chunk, a #cite{} key with no bibliography entry, a
bibliography entry missing author, title or year.
Warnings (exit status 0): chunks defined but never used, empty chunks, long stretches of code with
no prose, bibliography entries nobody cites, entries with nothing to verify them against
(no arXiv id, DOI, ISBN or URL).

--refs prints one line per cited reference (key, authors, year, title, identifier): the checklist
to verify against the real papers before delivering the document.  Finally a size summary
(chunks, code lines, prose words, citations) helps judge the balance of prose and code.
"""
import contextlib
import io
import os
import re
import sys

from tangle import FILE, REF, load
from weave import Doc, img_parts, parse_bibtex, bib_parts, run_text, walk

LONG_CODE = 40          # lines of code in one chunk, without any prose, before we warn


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    if not args:
        sys.exit(__doc__)
    show_refs = "--refs" in sys.argv
    src = args[0]
    root = load(src)                                   # exits with a located message if the .lit is malformed
    with contextlib.redirect_stderr(io.StringIO()):    # Doc warns about the same things we report below
        doc = Doc(root, os.path.basename(src), os.path.dirname(src))
    errors, warnings = [], []

    # ---- figures
    for chunk, it in walk(doc.root):
        if it[0] == "img" and doc.image(img_parts(it[1])[0]) is None:
            errors.append(f"#img{{{img_parts(it[1])[0]}}}: neither a file chunk of this document nor a file next to it")

    # ---- chunks
    code_lines = prose_words = 0
    per_chunk = {}
    for chunk, it in walk(doc.root):
        if it[0] == "code" and chunk.name:
            per_chunk.setdefault(chunk.name, {"code": 0, "prose": 0})["code"] += len(it[1])
            code_lines += len(it[1])
        elif it[0] == "prose" and chunk.name:
            words = len(re.sub("[\ue000-\ue003]", " ", run_text(it[1])[0]).split())
            per_chunk.setdefault(chunk.name, {"code": 0, "prose": 0})["prose"] += words
            prose_words += words
        elif it[0] == "prose":
            prose_words += len(re.sub("[\ue000-\ue003]", " ", run_text(it[1])[0]).split())
    for name, cs in doc.defs.items():
        stats = per_chunk.get(name, {"code": 0, "prose": 0})
        if name not in doc.uses and not FILE.search(name) and stats["code"]:
            warnings.append(f"chunk \u00ab{name}\u00bb has code but is never referenced, so it is never written to a file")
        if not stats["code"] and not stats["prose"] and not FILE.search(name):
            warnings.append(f"chunk \u00ab{name}\u00bb is empty")
        if stats["code"] > LONG_CODE and stats["prose"] == 0:
            warnings.append(f"chunk \u00ab{name}\u00bb has {stats['code']} lines of code and no prose; "
                            "split it into named steps and explain them")
    for chunk, it in walk(doc.root):                # hints for an agent that are still open
        if it[0] == "hint":
            warnings.append(f"an agent hint is still open in \u00ab{chunk.name}\u00bb: {it[1][:60]}")
    for name in doc.uses:
        if name not in doc.defs:
            errors.append(f"<<<{name}>>> is referenced but no chunk of that name is defined")

    # ---- citations and bibliography
    for key in doc.cite_no:
        if key not in doc.bibsrc:
            errors.append(f"#cite{{{key}}} has no entry in #bibliography{{}}")
    rows = []
    for key, src_text in doc.bibsrc.items():
        fields = parse_bibtex(src_text)
        if key not in doc.cite_no:
            warnings.append(f"bibliography entry '{key}' is never cited")
        missing = [f for f in ("author", "title", "year") if not fields.get(f, "").strip()]
        if missing:
            errors.append(f"bibliography entry '{key}' lacks: {', '.join(missing)}")
        p = bib_parts(fields)
        ident = (f"arXiv:{p['arxiv']}" if p["arxiv"] else "") or (f"doi:{fields['doi']}" if fields.get("doi") else "") \
            or (f"isbn:{fields['isbn']}" if fields.get("isbn") else "") or p["url"]
        if not ident:
            warnings.append(f"bibliography entry '{key}' has no arXiv id, DOI, ISBN or URL; check it by title and authors")
        rows.append((key, p["authors"], p["year"], p["title"], ident or "(no identifier)"))

    # ---- report
    if show_refs:
        print("references to verify (author, year, title, identifier):")
        for key, authors, year, title, ident in rows:
            print(f"  {key:28s} {authors[:46]:46s} {year}  {title[:70]}  {ident}")
        print()
    for w in warnings:
        print("warning:", w)
    for e in errors:
        print("error:", e)
    print(f"{len(doc.defs)} chunks, {code_lines} code lines, {prose_words} words of prose, "
          f"{len(doc.cite_no)} citations, {len(doc.bibsrc)} bibliography entries; "
          f"{len(errors)} error(s), {len(warnings)} warning(s)")
    sys.exit(1 if errors else 0)


if __name__ == "__main__":
    main()
