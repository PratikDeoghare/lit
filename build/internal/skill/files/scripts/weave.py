#!/usr/bin/env python3
"""Weave a brashtag literate source into interlinked HTML (MathJax) and LaTeX.

    python weave.py llm-rig.lit [-o outdir] [--only html|tex] [--mathjax URL] [--highlight]

Writes <outdir>/<name>.html and <outdir>/<name>.tex. No PDF is produced here: compile the
LaTeX yourself, e.g.   xelatex llm-rig.tex   (twice; pdflatex cannot read all of Unicode).

Needs the brashtag parser (pip install git+https://github.com/PratikDeoghare/brashtag.git)
and tangle.py next to this file, which defines how the notation is read.
Optional: pygments, with --highlight (syntax colours in HTML and LaTeX). Without it code is written as plain
text, so the .html and .tex stay readable as source.

What the woven document contains
  * prose, headings (#h1{} .. #h6{}), a table of contents, and the code in document order
  * every chunk numbered (§n); every <<<reference>>> inside code links to the chunk it
    expands, and every chunk lists where it is used and which files it ends up in
  * #cite{key} (or #cite{a,b}) as numbered, linked citations, and a bibliography built from
    #bibliography{...} whose entries link back to the chunks that cite them
  * a chunk index
  * figures: #img{`diagram.svg` A caption.} is a figure. The path names a file chunk of the program
    (so the picture can be part of the literate source) or a file next to the root .lit.  HTML inlines
    SVG and embeds other images; LaTeX gets a PDF (SVG is converted with rsvg-convert or inkscape if
    one is installed; PNG and JPEG are copied beside the .tex)
  * math: an inline code span of the form `$...$` is LaTeX math; a paragraph consisting of
    nothing but one such span is display math.  HTML: typeset by MathJax (loaded from a CDN,
    or from --mathjax).  LaTeX: passed through unchanged.

LaTeX needs: amsmath amssymb xcolor fancyvrb hyperref longtable iftex (all standard);
fvextra is used for line breaking if installed.  Use xelatex or lualatex (pdflatex cannot read all of Unicode).
"""
import argparse
import base64
import html
import mimetypes
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unicodedata

from tangle import REF, FILE, HEADINGS, HINT, Bag, Blob, Code, Source, collect, expand, fence_text, is_fence, unescape

MATHJAX = "https://cdn.jsdelivr.net/npm/mathjax@3.2.2/es5/tex-svg.js"

# Inline pieces are swapped for private-use placeholders while prose is split into
# paragraphs and lists, then substituted back as HTML or LaTeX.
CITE_RUN = re.compile("\ue002[^\ue003]*\ue003(?:\\s+\ue002[^\ue003]*\ue003)*")
CITE_ONE = re.compile("\ue002([^\ue003]*)\ue003")
CODE_PH = re.compile("\ue000(\\d+)\ue001")
QUOTES = re.compile(r'"([^"]*)"')
esc = lambda s: html.escape(s, quote=False)


# ============================================================ document tree
class Chunk:
    def __init__(self, name, parent):
        self.name, self.parent, self.items = name, parent, []
        self.num = self.id = None
        self.refs = []          # chunk names referenced from this chunk's code


def plain(kids):
    return "".join(k.text for k in kids if isinstance(k, (Blob, Code))).strip()


def run_text(kids):
    """Inline kids -> (text with placeholders, table of Code spans)."""
    text, table = [], []
    for k in kids:
        if isinstance(k, Blob):
            text.append(k.text)
        elif isinstance(k, Code):
            table.append(k)
            text.append(f"\ue000{len(table) - 1}\ue001")
        elif isinstance(k, Bag):                       # #cite{...}
            text.append(f"\ue002{plain(k.kids)}\ue003")
    return "".join(text), table


def math_of(code):
    mm = re.fullmatch(r"\s*\$(.+)\$\s*", code.text, re.S)
    return mm.group(1).strip() if mm else None


def build(bag, parent):
    """Brashtag tree -> Chunk tree.
    Items: ("prose", kids) | ("code", lines) | ("head", level, kids) | ("chunk", Chunk) | ("bib", Bag)."""
    c = Chunk(None if parent is None else bag.tag.strip(), parent)
    run = []

    def flush():
        if run:
            c.items.append(("prose", list(run)))
            run.clear()

    for k in bag.kids:
        if isinstance(k, Code) and is_fence(k):
            flush()
            c.items.append(("code", fence_text(k)))
        elif isinstance(k, Bag):
            tag = k.tag.strip()
            if tag == "cite":
                run.append(k)                           # inline
            elif tag == "img":
                flush()
                c.items.append(("img", k))
            elif tag == HINT:
                flush()
                c.items.append(("hint", " ".join(plain(k.kids).split())))
            elif tag in HEADINGS:
                flush()
                c.items.append(("head", int(tag[1]), k.kids))
            elif tag == "bibliography":
                flush()
                c.items.append(("bib", k))
            else:
                flush()
                c.items.append(("chunk", build(k, c)))
        else:
            run.append(k)                               # Blob or inline Code
    flush()
    return c


def img_parts(bag):
    """#img{`path` caption} (or #img{path caption}) -> (path, caption)."""
    code = next((k for k in bag.kids if isinstance(k, Code)), None)
    rest = " ".join("".join(k.text for k in bag.kids if isinstance(k, Blob)).split())
    if code is not None:
        return code.text.strip(), rest
    path, _, cap = rest.partition(" ")
    return path, cap.strip()


def walk(chunk):
    """Depth-first, document order: yields (chunk, item)."""
    for it in chunk.items:
        yield chunk, it
        if it[0] == "chunk":
            yield from walk(it[1])


def slug(s):
    return re.sub(r"[^A-Za-z0-9]+", "-", s).strip("-") or "x"


# ============================================================ bibtex
ACCENTS = {"'": "\u0301", "`": "\u0300", '"': "\u0308", "^": "\u0302", "~": "\u0303"}


def tex_text(s):
    s = re.sub(r"\\([\'`\"^~])\{?(\w)\}?", lambda m: unicodedata.normalize("NFC", m.group(2) + ACCENTS[m.group(1)]), s)
    s = s.replace("--", "\u2013").replace(r"\&", "&")
    return re.sub(r"[{}]", "", s).strip()


def parse_bibtex(src):
    m = re.match(r"\s*@(\w+)\s*\{\s*([^,]+),", src)
    pos, fields = (m.end() if m else 0), {}
    while True:
        fm = re.compile(r"\s*(\w+)\s*=\s*").search(src, pos)
        if not fm:
            break
        p = fm.end()
        if p < len(src) and src[p] == "{":
            depth, j = 0, p
            while j < len(src):
                depth += (src[j] == "{") - (src[j] == "}")
                if depth == 0:
                    break
                j += 1
            val, pos = src[p + 1:j], j + 1
        else:
            nm = re.match(r"[^,}\s]+", src[p:])
            val = nm.group(0) if nm else ""
            pos = p + len(val)
        fields[fm.group(1).lower()] = val
    return fields


def bib_parts(f):
    names = [n.strip() for n in re.split(r"\s+and\s+", tex_text(f.get("author", "")))]
    if names and names[-1] == "others":
        authors = ", ".join(names[:-1]) + " et al."
    elif len(names) > 1:
        authors = ", ".join(names[:-1]) + " and " + names[-1]
    else:
        authors = names[0] if names and names[0] else ""
    venue = re.sub(r"\s*arXiv:\S+", "", f.get("journal") or f.get("booktitle") or f.get("institution")
                   or f.get("publisher") or "")
    hp = f.get("howpublished", "")
    um = re.search(r"\\url\{([^}]+)\}", hp)
    am = re.search(r"arXiv:([\w./-]+)", f.get("note", "") + " " + f.get("journal", ""))
    return dict(authors=authors, title=tex_text(f.get("title", "")), venue=tex_text(venue),
                vol=f.get("volume", "") + (f"({f['number']})" if f.get("number") else ""),
                pages=tex_text(f.get("pages", "")), year=f.get("year", ""),
                url=um.group(1) if um else "", hp="" if um else tex_text(hp), arxiv=am.group(1) if am else "")


def _stop(s):                       # add a full stop unless the text already ends in punctuation
    return s if s[-1:] in ".?!" else s + "."


# ============================================================ analysis (shared by both backends)
class Doc:
    def __init__(self, root_bag, src_name, base="."):
        self.src_name, self.base, self.bag, self._files = src_name, base or ".", root_bag, None
        self.root = build(root_bag, None)
        self.defs, self.uses, self.partof, self.lang = {}, {}, {}, {}
        self.bibsrc, self.cite_no, self.cited_in, self.heads = {}, {}, {}, []
        self.used_ids, self.head_id, self.head_title = set(), {}, {}
        self._analyse()
        self.title = next((t for lvl, t, _ in self.heads if lvl == 1), os.path.splitext(src_name)[0])

    def image(self, path):
        """('text', content) for a file chunk, ('file', path) for a file on disk, or None."""
        if self._files is None:
            import contextlib, io
            with contextlib.redirect_stderr(io.StringIO()):
                chunks = collect(self.bag)
            self._files = {n: "\n".join(expand(n, chunks)) + "\n" for n in chunks if FILE.search(n)}
        if path in self._files:
            return "text", self._files[path]
        full = os.path.join(self.base, path)
        return ("file", full) if os.path.isfile(full) else None

    def unique_id(self, base):
        i, cand = 1, base
        while cand in self.used_ids:
            i += 1
            cand = f"{base}-{i}"
        self.used_ids.add(cand)
        return cand

    @staticmethod
    def cite_keys(text):
        return [k.strip() for g in CITE_ONE.findall(text) for k in g.split(",") if k.strip()]

    def _analyse(self):
        n = 0
        for chunk, it in walk(self.root):
            if it[0] == "head":
                title = run_text(it[2])[0].strip()
                hid = self.unique_id("h-" + slug(title))
                self.heads.append((it[1], title, hid))
                self.head_id[id(it)], self.head_title[hid] = hid, title
            elif it[0] == "chunk":
                n += 1
                c = it[1]
                c.num, c.id = n, self.unique_id("c-" + slug(c.name))
                self.defs.setdefault(c.name, []).append(c)
            elif it[0] == "bib":
                for k in it[1].kids:
                    if isinstance(k, Bag):
                        self.bibsrc[k.tag.strip()] = "\n".join(
                            ln for kk in k.kids if isinstance(kk, Code) and is_fence(kk) for ln in fence_text(kk))
        cur_head = None                                     # references, uses, citations
        for chunk, it in walk(self.root):
            if it[0] == "head":
                hid = self.head_id[id(it)]
                cur_head = (self.head_title[hid], hid)
            elif it[0] == "code":
                for ln in it[1]:
                    m = REF.match(ln)
                    if m:
                        name = m.group(2).strip()
                        chunk.refs.append(name)
                        lst = self.uses.setdefault(name, [])
                        if chunk not in lst:
                            lst.append(chunk)
            elif it[0] == "prose":
                for key in self.cite_keys(run_text(it[1])[0]):
                    if key not in self.cite_no:
                        self.cite_no[key] = len(self.cite_no) + 1
                    if chunk.name:
                        where = (chunk.name, chunk.id)
                    elif cur_head:
                        where = ("introduction" if cur_head[1] == self.heads[0][2] else cur_head[0], cur_head[1])
                    else:
                        where = None
                    if where and where not in self.cited_in.setdefault(key, []):
                        self.cited_in[key].append(where)
        for chunk, it in walk(self.root):
            if it[0] == "img" and self.image(img_parts(it[1])[0]) is None:
                print(f"warning: #img{{{img_parts(it[1])[0]}}}: no such file chunk or file", file=sys.stderr)
        for name in self.uses:
            if name not in self.defs:
                print(f"warning: <<<{name}>>> is referenced but never defined", file=sys.stderr)
        for f in [n for n in self.defs if FILE.search(n)]:  # which files each chunk ends up in
            seen, queue = {f}, [f]
            self.lang.setdefault(f, os.path.splitext(f)[1])
            while queue:
                cur = queue.pop(0)
                for c in self.defs.get(cur, []):
                    for r in c.refs:
                        if r in self.defs and r not in seen:
                            seen.add(r)
                            queue.append(r)
                            self.partof.setdefault(r, []).append(f)
                            self.lang.setdefault(r, os.path.splitext(f)[1])
        for key in self.cite_no:
            if key not in self.bibsrc:
                print(f"warning: #cite{{{key}}} has no bibliography entry", file=sys.stderr)

    def blocks(self, kids):
        """Prose run -> ([("p", text) | ("ul", [text]) | ("math", tex)], table); text has placeholders."""
        text, table = run_text(kids)
        paras, cur = [], []
        for ln in text.split("\n") + [""]:
            if ln.strip():
                cur.append(ln.strip())
            elif cur:
                paras.append(cur)
                cur = []
        out = []
        for p in paras:
            if p[0].startswith("- "):
                items = []
                for ln in p:
                    if ln.startswith("- "):
                        items.append(ln[2:])
                    else:
                        items[-1] += " " + ln
                out.append(("ul", items))
                continue
            t = " ".join(p)
            m = CODE_PH.fullmatch(t)
            tex = math_of(table[int(m.group(1))]) if m else None
            out.append(("math", tex) if tex is not None else ("p", t))
        return out, table

    def users(self, c):
        return [u for u in self.uses.get(c.name, []) if u is not c]


HIGHLIGHT = False        # --highlight: colour code with pygments; the markup it adds makes .tex and .html hard to read


def pygments_tools():
    if not HIGHLIGHT:
        return None
    try:
        from pygments import highlight
        from pygments.formatters import HtmlFormatter, LatexFormatter
        from pygments.lexers import get_lexer_by_name, get_lexer_for_filename
    except ImportError:
        print("note: pygments not found; code is not syntax-highlighted", file=sys.stderr)
        return None

    def lexer(ext):
        try:
            if ext in (".md", ".txt", ""):
                return get_lexer_by_name("text", stripnl=False, ensurenl=False)
            return get_lexer_for_filename("x" + ext, stripnl=False, ensurenl=False)
        except Exception:
            return get_lexer_by_name("text", stripnl=False, ensurenl=False)

    return dict(highlight=highlight, Html=HtmlFormatter, Latex=LatexFormatter, lexer=lexer)


# ============================================================ HTML backend (MathJax)
class HtmlWeaver:
    def __init__(self, doc, mathjax):
        self.d, self.mathjax, self.P = doc, mathjax, pygments_tools()

    # ---- inline
    def cite_html(self, m):
        d = self.d
        keys = d.cite_keys(m.group(0))
        links = [f'<a href="#bib-{k}">{d.cite_no[k]}</a>' if k in d.bibsrc else f"?{esc(k)}" for k in keys]
        return '<span class="cite">[' + ", ".join(links) + "]</span>"

    @staticmethod
    def math_html(tex, display=False):
        body = esc(tex)
        return f'<div class="display">\\[{body}\\]</div>' if display else f'<span class="math">\\({body}\\)</span>'

    def code_span(self, k):
        tex = math_of(k)
        return self.math_html(tex) if tex is not None else f"<code>{esc(k.text)}</code>"

    def fill(self, s, table):
        s = QUOTES.sub("\u201c\\1\u201d", esc(s))
        s = CITE_RUN.sub(self.cite_html, s)
        return CODE_PH.sub(lambda m: self.code_span(table[int(m.group(1))]), s)

    def inline(self, kids):
        text, table = run_text(kids)
        return self.fill(text.strip(), table)

    def prose(self, kids):
        blocks, table = self.d.blocks(kids)
        out = []
        for kind, val in blocks:
            if kind == "ul":
                out.append("<ul>" + "".join(f"<li>{self.fill(i, table)}</li>" for i in val) + "</ul>")
            elif kind == "math":
                out.append(self.math_html(val, display=True))
            else:
                out.append(f"<p>{self.fill(val, table)}</p>")
        return "\n".join(out)

    # ---- code
    def highlight(self, code, ext):
        if not self.P or not code:
            return esc(code)
        return self.P["highlight"](code, self.P["lexer"](ext), self.P["Html"](nowrap=True))

    def ref_link(self, name, indent):
        d = self.d
        if name not in d.defs:
            return f'{esc(indent)}<span class="ref bad">\u00ab{esc(name)}\u00bb</span>'
        c = d.defs[name][0]
        return (f'{esc(indent)}<a class="ref pg" href="#{c.id}">\u00ab{esc(name)}\u00bb'
                f'<span class="rn">\u00a7{c.num}</span></a>')

    def code(self, lines, chunk):
        ext, segs, buf = self.d.lang.get(chunk.name, ""), [], []
        for ln in lines:
            m = REF.match(ln)
            if m:
                if buf:
                    segs.append(self.highlight("\n".join(buf), ext))
                    buf = []
                segs.append(self.ref_link(m.group(2).strip(), m.group(1)))
            else:
                buf.append(unescape(ln))
        if buf:
            segs.append(self.highlight("\n".join(buf), ext))
        return '<pre class="code">' + "\n".join(segs) + "</pre>"

    # ---- structure
    @staticmethod
    def label(name):
        return esc(name) if FILE.search(name) else f"\u00ab{esc(name)}\u00bb"

    def chunk_link(self, c):
        return f'<a class="pg" href="#{c.id}">{self.label(c.name)} \u00a7{c.num}</a>'

    def meta(self, c):
        d, bits = self.d, []
        users = d.users(c)
        if users:
            bits.append("used in " + ", ".join(self.chunk_link(u) for u in users))
        if not FILE.search(c.name) and d.partof.get(c.name):
            bits.append("part of " + ", ".join(f'<a href="#{d.defs[f][0].id}">{esc(f)}</a>' for f in d.partof[c.name]))
        sep = " \u00b7 ".join(bits)
        return f'<div class="meta">{sep}</div>' if bits else ""

    def chunk(self, c, depth):
        isfile = bool(FILE.search(c.name))
        title = (f'<span class="badge">file</span> <span class="fname">{esc(c.name)}</span>' if isfile
                 else f'<span class="cname">\u00ab{esc(c.name)}\u00bb</span> <span class="eq">\u2261</span>')
        h = [f'<section class="chunk{" nested" if depth else ""}" id="{c.id}">',
             f'<div class="chunk-head"><a class="num" href="#{c.id}">\u00a7{c.num}</a> {title}</div>', self.meta(c)]
        for it in c.items:
            if it[0] == "prose":
                h.append(self.prose(it[1]))
            elif it[0] == "code":
                h.append(self.code(it[1], c))
            elif it[0] == "chunk":
                h.append(self.chunk(it[1], depth + 1))
            elif it[0] == "head":
                h.append(self.heading(it))
            elif it[0] == "img":
                h.append(self.figure(it))
            elif it[0] == "hint":
                h.append(self.hint(it))
        h.append("</section>")
        return "\n".join(x for x in h if x)

    def heading(self, it):
        return f'<h{it[1]} id="{self.d.head_id[id(it)]}">{self.inline(it[2])}</h{it[1]}>'

    def hint(self, it):
        return '<p class="hint"><b>to be filled in by an agent:</b> ' + esc(it[1]) + "</p>"

    def figure(self, it):
        path, cap = img_parts(it[1])
        found = self.d.image(path)
        if found is None:
            return f'<p class="bad">missing figure {esc(path)}</p>'
        kind, val = found
        ext = os.path.splitext(path)[1].lower()
        if ext == ".svg":
            svg = val if kind == "text" else open(val, encoding="utf-8").read()
            svg = re.sub(r"<\?xml.*?\?>|<!DOCTYPE.*?>", "", svg, flags=re.S).strip()
            body = svg.replace("<svg", f'<svg role="img" aria-label="{html.escape(cap or path)}"', 1)
        else:
            data = val.encode() if kind == "text" else open(val, "rb").read()
            mime = mimetypes.guess_type(path)[0] or "application/octet-stream"
            body = f'<img alt="{html.escape(cap or path)}" src="data:{mime};base64,{base64.b64encode(data).decode()}">'
        return f'<figure class="fig">{body}' + (f"<figcaption>{esc(cap)}</figcaption>" if cap else "") + "</figure>"

    def bibliography(self):
        d, rows = self.d, []
        for key in sorted(d.cite_no, key=d.cite_no.get):
            if key not in d.bibsrc:
                continue
            p = bib_parts(parse_bibtex(d.bibsrc[key]))
            parts = [_stop(esc(p["authors"])) if p["authors"] else "", f"\u201c{_stop(esc(p['title']))}\u201d"]
            tail = ", ".join(x for x in [f"<i>{esc(p['venue'])}</i>" if p["venue"].strip() else "",
                                         esc(p["vol"]), esc(p["pages"]), esc(p["year"])] if x)
            parts.append(tail + "." if tail else "")
            if p["url"]:
                parts.append(f'<a href="{p["url"]}">{esc(p["url"])}</a>')
            elif p["hp"]:
                parts.append(_stop(esc(p["hp"])))
            if p["arxiv"]:
                parts.append(f'<a href="https://arxiv.org/abs/{p["arxiv"]}">arXiv:{p["arxiv"]}</a>')
            back = ", ".join(f'<a class="pg" href="#{i}">{self.label(n)}</a>' if i.startswith("c-") else
                             f'<a href="#{i}">{esc(n)}</a>' for n, i in d.cited_in.get(key, []))
            rows.append(f'<li id="bib-{key}" value="{d.cite_no[key]}">{" ".join(x for x in parts if x)}'
                        f'<div class="meta">cited in {back}</div></li>')
        return ('<section class="backmatter" id="bibliography"><h2>Bibliography</h2>\n<ol class="bib">'
                + "\n".join(rows) + "</ol></section>")

    def index(self):
        d, rows = self.d, []
        for name in sorted(d.defs, key=str.lower):
            c = d.defs[name][0]
            users = ", ".join(f'<a class="pg" href="#{u.id}">\u00a7{u.num}</a>' for u in d.users(c))
            rows.append(f'<tr><td><a class="pg" href="#{c.id}">{self.label(name)}</a></td>'
                        f'<td class="n">\u00a7{c.num}</td><td>{users}</td></tr>')
        return ('<section class="backmatter" id="index"><h2>Chunk index</h2>\n<table class="index"><thead><tr><th>chunk</th>'
                '<th>defined</th><th>used in</th></tr></thead><tbody>' + "\n".join(rows) + "</tbody></table></section>")

    def render(self):
        d, body, first_h1 = self.d, [], True
        for it in d.root.items:
            if it[0] == "head":
                if it[1] == 1 and first_h1:
                    first_h1 = False
                    body.append(f'<h1 class="title" id="{d.head_id[id(it)]}">{self.inline(it[2])}</h1>'
                                f'<p class="sub">Woven from <code>{esc(d.src_name)}</code> \u2014 '
                                f'code chunks are numbered and cross-linked</p>@@CONTENTS@@')
                else:
                    body.append(self.heading(it))
            elif it[0] == "prose":
                body.append(self.prose(it[1]))
            elif it[0] == "code":
                body.append(self.code(it[1], d.root))
            elif it[0] == "chunk":
                body.append(self.chunk(it[1], 0))
            elif it[0] == "img":
                body.append(self.figure(it))
            elif it[0] == "hint":
                body.append(self.hint(it))
        body += [self.index(), self.bibliography()]
        toc = [(lvl, t, i) for lvl, t, i in d.heads if not (lvl == 1 and t == d.title)]
        toc += [(2, "Chunk index", "index"), (2, "Bibliography", "bibliography")]
        toc_html = "".join(f'<li class="l{lvl}"><a class="pg" href="#{i}">{esc(t)}</a></li>' for lvl, t, i in toc)
        files = [n for n in d.defs if FILE.search(n)]
        files_html = "".join(f'<li><a class="pg" href="#{d.defs[n][0].id}">{esc(n)}</a></li>' for n in files)
        contents = f'<nav id="contents"><h2>Contents</h2><ul>{toc_html}</ul><h3>Files</h3><ul class="files">{files_html}</ul></nav>'
        side = (f'<nav id="side"><div class="st">{esc(d.title)}</div><ul>{toc_html}</ul>'
                f'<div class="st">Files</div><ul class="files">{files_html}</ul></nav>')
        css = CSS + ("\n" + self.P["Html"](style="default").get_style_defs("pre.code") if self.P else "")
        mj = ('<script>window.MathJax={tex:{inlineMath:[["\\\\(","\\\\)"]],displayMath:[["\\\\[","\\\\]"]]},'
              'options:{skipHtmlTags:["script","noscript","style","textarea","pre","code"]}};</script>'
              f'<script async src="{html.escape(self.mathjax)}"></script>')
        return (f'<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" '
                f'content="width=device-width,initial-scale=1"><title>{esc(d.title)}</title>{mj}'
                f'<style>{css}</style></head><body>{side}<main>'
                + "\n".join(body).replace("@@CONTENTS@@", contents) + "</main></body></html>")


# ============================================================ LaTeX backend
TEX_SPECIAL = {"\\": r"\textbackslash{}", "{": r"\{", "}": r"\}", "$": r"\$", "&": r"\&", "#": r"\#", "_": r"\_",
               "%": r"\%", "~": r"\textasciitilde{}", "^": r"\textasciicircum{}", "<": r"\textless{}",
               ">": r"\textgreater{}", "|": r"\textbar{}"}


def tex_esc(s):
    return "".join(TEX_SPECIAL.get(ch, ch) for ch in s)


def tex_code_esc(s):                    # for \texttt: allow line breaks after _ / .
    return re.sub(r"(\\_|/|\.)", r"\1\\allowbreak{}", tex_esc(s))


def verb_esc(s):                        # inside Verbatim with commandchars=\\\{\}
    return s.replace("\\", r"\textbackslash{}").replace("{", r"\{").replace("}", r"\}")


PREAMBLE = r"""\documentclass[10pt,a4paper]{article}
\usepackage{iftex}
\ifPDFTeX
  \usepackage[utf8]{inputenc}
  \usepackage[T1]{fontenc}
  \IfFileExists{lmodern.sty}{\usepackage{lmodern}}{}
  \DeclareUnicodeCharacter{2261}{\ensuremath{\equiv}}
\else
  \usepackage{fontspec}
  \IfFontExistsTF{TeX Gyre Pagella}{\setmainfont{TeX Gyre Pagella}}{\IfFontExistsTF{texgyrepagella-regular.otf}{\setmainfont{texgyrepagella-regular.otf}[BoldFont=texgyrepagella-bold.otf,ItalicFont=texgyrepagella-italic.otf,BoldItalicFont=texgyrepagella-bolditalic.otf]}{\IfFontExistsTF{STIX Two Text}{\setmainfont{STIX Two Text}}{}}}
  \IfFontExistsTF{DejaVu Sans Mono}{\setmonofont{DejaVu Sans Mono}[Scale=MatchLowercase]}{}
\fi
\usepackage[a4paper,left=24mm,textwidth=118mm,marginparwidth=58mm,marginparsep=6mm,top=24mm,bottom=26mm]{geometry}
\usepackage{amsmath,amssymb}
\usepackage[dvipsnames]{xcolor}
\definecolor{tufteink}{HTML}{111111}
\definecolor{tuftepaper}{HTML}{FFFFF8}
\definecolor{tufteaccent}{HTML}{8A2A2A}
\definecolor{tuftegray}{HTML}{6F6F66}
\AtBeginDocument{\pagecolor{tuftepaper}\color{tufteink}}
\usepackage{fancyvrb}
\IfFileExists{fvextra.sty}{\usepackage{fvextra}\fvset{breaklines=true}}{}
\usepackage{longtable}
\usepackage{graphicx}
\usepackage[colorlinks=true,linkcolor=tufteaccent,citecolor=tufteaccent,urlcolor=tufteaccent]{hyperref}
\fvset{commandchars=\\\{\},fontsize=\scriptsize,frame=leftline,framesep=3mm,rulecolor=\color{tuftegray!45}}
@@STYLE@@
\setlength{\parindent}{0pt}
\setlength{\parskip}{0.55em plus 0.15em}
\linespread{1.06}
\emergencystretch=2em
\makeatletter
\renewcommand\section{\@startsection{section}{1}{\z@}{-3.2ex \@plus -1ex \@minus -.2ex}{1.2ex \@plus .2ex}{\normalfont\LARGE\itshape}}
\renewcommand\subsection{\@startsection{subsection}{2}{\z@}{-2.6ex \@plus -1ex \@minus -.2ex}{1ex \@plus .2ex}{\normalfont\Large\itshape}}
\renewcommand\subsubsection{\@startsection{subsubsection}{3}{\z@}{-2.2ex \@plus -1ex \@minus -.2ex}{.8ex \@plus .2ex}{\normalfont\large\itshape}}
\makeatother
\newcounter{chunk}
\renewcommand{\thechunk}{\S\arabic{chunk}}
\newcommand{\chunkhead}[2]{\par\addvspace{1.2ex}\noindent\refstepcounter{chunk}\label{#1}{\bfseries\thechunk}\enspace #2\par\nopagebreak}
\newcommand{\chunkmeta}[1]{\marginpar{\raggedright\scriptsize\itshape\color{tuftegray}#1}\nopagebreak}
\newcommand{\chunkname}[1]{\ensuremath{\langle}\texttt{#1}\ensuremath{\rangle}}
\newcommand{\chunklink}[2]{\hyperref[#1]{#2~\ref*{#1}}\ (p.~\pageref*{#1})}
\newcommand{\chunkrefv}[1]{\hyperref[#1]{\ensuremath{\langle}\csname cn@#1\endcsname\ensuremath{\rangle}\textsuperscript{\ref*{#1}}}}
\newcommand{\filebadge}{{\footnotesize\itshape\color{tuftegray}file}}
\newenvironment{nested}{\begin{list}{}{\setlength{\leftmargin}{1.1em}\setlength{\rightmargin}{0pt}\setlength{\topsep}{0pt}\setlength{\partopsep}{0pt}\setlength{\parsep}{\parskip}\setlength{\itemsep}{0pt}}\item[]}{\end{list}}
\hypersetup{pdftitle={@@PDFTITLE@@}}
\title{@@TITLE@@\\[0.4em]\large Woven from \texttt{@@SOURCE@@}}
\author{}
\date{}
@@NAMES@@
\begin{document}
\maketitle
\tableofcontents
"""


class TexWeaver:
    def __init__(self, doc, outdir="."):
        self.d, self.P, self.outdir = doc, pygments_tools(), outdir
        self.lab = {c.id: f"chunk{c.num}" for cs in doc.defs.values() for c in cs}   # hyphen-free: safe in verbatim
        mins = [lvl for lvl, t, _ in doc.heads if not (lvl == 1 and t == doc.title)]
        self.min_level = min(mins) if mins else 1

    # ---- inline
    def label(self, name):
        return rf"\texttt{{{tex_code_esc(name)}}}" if FILE.search(name) else rf"\chunkname{{{tex_code_esc(name)}}}"

    def cite_tex(self, m):
        keys = self.d.cite_keys(m.group(0))
        known = [k for k in keys if k in self.d.bibsrc]
        out = [rf"\cite{{{','.join(known)}}}"] if known else []
        out += [rf"\textbf{{?{tex_esc(k)}}}" for k in keys if k not in self.d.bibsrc]
        return "".join(out)

    @staticmethod
    def code_span(k):
        tex = math_of(k)
        return rf"\({tex}\)" if tex is not None else rf"\texttt{{{tex_code_esc(k.text)}}}"

    def fill(self, s, table):
        s = QUOTES.sub(lambda m: "``" + m.group(1) + "''", tex_esc(s))
        s = CITE_RUN.sub(self.cite_tex, s)
        return CODE_PH.sub(lambda m: self.code_span(table[int(m.group(1))]), s)

    def inline(self, kids):
        text, table = run_text(kids)
        return self.fill(text.strip(), table)

    def prose(self, kids):
        blocks, table = self.d.blocks(kids)
        out = []
        for kind, val in blocks:
            if kind == "ul":
                out.append("\\begin{itemize}\n" + "\n".join(f"\\item {self.fill(i, table)}" for i in val) + "\n\\end{itemize}")
            elif kind == "math":
                out.append(f"\\[\n{val}\n\\]")
            else:
                out.append(self.fill(val, table))
        return "\n\n".join(out)

    # ---- code
    def highlight(self, code, ext):
        if not code:
            return ""
        if self.P:
            return self.P["highlight"](code, self.P["lexer"](ext), self.P["Latex"](nowrap=True))
        return verb_esc(code)

    def code(self, lines, chunk):
        if not lines:
            return ""
        d, ext, segs, buf = self.d, self.d.lang.get(chunk.name, ""), [], []
        for ln in lines:
            m = REF.match(ln)
            if m:
                if buf:
                    segs.append(self.highlight("\n".join(buf), ext))
                    buf = []
                name = m.group(2).strip()
                segs.append(m.group(1) + (rf"\chunkrefv{{{self.lab[d.defs[name][0].id]}}}" if name in d.defs else r"\textbf{??}"))
            else:
                buf.append(unescape(ln))
        if buf:
            segs.append(self.highlight("\n".join(buf), ext))
        return "\\begin{Verbatim}\n" + "\n".join(segs) + "\n\\end{Verbatim}"

    # ---- structure
    def link(self, c):
        return rf"\chunklink{{{self.lab[c.id]}}}{{{self.label(c.name)}}}"

    def meta(self, c):
        d, bits = self.d, []
        users = d.users(c)
        if users:
            bits.append("used in " + ", ".join(self.link(u) for u in users))
        if not FILE.search(c.name) and d.partof.get(c.name):
            bits.append("part of " + ", ".join(rf"\hyperref[{self.lab[d.defs[f][0].id]}]{{\texttt{{{tex_code_esc(f)}}}}}" for f in d.partof[c.name]))
        sep = " \\textperiodcentered{} ".join(bits)
        return rf"\chunkmeta{{{sep}}}" if bits else ""

    def chunk(self, c, depth):
        isfile = bool(FILE.search(c.name))
        title = (rf"\filebadge\ \textbf{{\texttt{{{tex_code_esc(c.name)}}}}}" if isfile
                 else rf"\textbf{{\ensuremath{{\langle}}\texttt{{{tex_code_esc(c.name)}}}\ensuremath{{\rangle}}}}\enspace\ensuremath{{\equiv}}")
        parts = [r"\begin{nested}"] if depth else []
        parts += [rf"\chunkhead{{{self.lab[c.id]}}}{{{title}}}", self.meta(c)]
        for it in c.items:
            if it[0] == "prose":
                parts.append(self.prose(it[1]))
            elif it[0] == "code":
                parts.append(self.code(it[1], c))
            elif it[0] == "chunk":
                parts.append(self.chunk(it[1], depth + 1))
            elif it[0] == "head":
                parts.append(self.heading(it))
            elif it[0] == "img":
                parts.append(self.figure(it))
            elif it[0] == "hint":
                parts.append(self.hint(it))
        if depth:
            parts.append(r"\end{nested}")
        return "\n".join(p for p in parts if p)

    def heading(self, it):
        d = self.d
        hid, depth = d.head_id[id(it)], min(it[1] - self.min_level, 3)
        cmd, kind = [("section", "section"), ("subsection", "subsection"), ("subsubsection", "subsubsection"),
                     ("paragraph", "paragraph")][max(depth, 0)]
        return (rf"\hypertarget{{{hid}}}{{}}\phantomsection\addcontentsline{{toc}}{{{kind}}}{{{tex_esc(d.head_title[hid])}}}"
                "\n" rf"\{cmd}*{{{self.inline(it[2])}}}")

    def hint(self, it):
        return r"\par\noindent{\small\textbf{to be filled in by an agent:} " + tex_esc(it[1]) + "}"

    def figure(self, it):
        """A figure environment; the picture is written beside the .tex as a PDF (or copied, if raster)."""
        path, cap = img_parts(it[1])
        found = self.d.image(path)
        stem, ext = os.path.splitext(os.path.basename(path))
        target = None
        if found is not None:
            kind, val = found
            os.makedirs(self.outdir, exist_ok=True)
            if ext.lower() == ".svg":
                tool = shutil.which("rsvg-convert") or shutil.which("inkscape")
                if tool:
                    with tempfile.TemporaryDirectory() as t:
                        src = os.path.join(t, stem + ".svg")
                        if kind == "text":
                            open(src, "w", encoding="utf-8").write(val)
                        else:
                            shutil.copy(val, src)
                        out = os.path.join(self.outdir, stem + ".pdf")
                        cmd = ([tool, "-f", "pdf", "-o", out, src] if tool.endswith("rsvg-convert")
                               else [tool, src, "--export-type=pdf", "--export-filename=" + out])
                        if subprocess.run(cmd, capture_output=True).returncode == 0:
                            target = stem + ".pdf"
                if target is None:
                    print(f"note: {path}: no rsvg-convert or inkscape, so the figure is left out of the LaTeX", file=sys.stderr)
            else:
                target = os.path.basename(path)
                if kind == "file":
                    shutil.copy(val, os.path.join(self.outdir, target))
                else:
                    open(os.path.join(self.outdir, target), "w", encoding="utf-8").write(val)
        pic = (rf"\includegraphics[width=\linewidth]{{{target}}}" if target
               else rf"\fbox{{\texttt{{{tex_code_esc(path)}}} (figure not available)}}")
        return ("\\begin{figure}[htbp]\n\\centering\n" + pic + "\n"
                + (rf"{{\small\itshape {tex_esc(cap)}\par}}" + "\n" if cap else "") + "\\end{figure}")

    def bibliography(self):
        d, items = self.d, []
        for key in sorted(d.cite_no, key=d.cite_no.get):
            if key not in d.bibsrc:
                continue
            p = bib_parts(parse_bibtex(d.bibsrc[key]))
            parts = [_stop(tex_esc(p["authors"])) if p["authors"] else "", "``" + _stop(tex_esc(p["title"])) + "''"]
            tail = ", ".join(x for x in [rf"\emph{{{tex_esc(p['venue'])}}}" if p["venue"].strip() else "",
                                         tex_esc(p["vol"]), tex_esc(p["pages"]), tex_esc(p["year"])] if x)
            parts.append(tail + "." if tail else "")
            if p["url"]:
                parts.append(rf"\url{{{p['url']}}}")
            elif p["hp"]:
                parts.append(_stop(tex_esc(p["hp"])))
            if p["arxiv"]:
                parts.append(rf"\href{{https://arxiv.org/abs/{p['arxiv']}}}{{arXiv:{p['arxiv']}}}")
            back = ", ".join(self.link_back(i, n) for n, i in d.cited_in.get(key, []))
            items.append(rf"\bibitem{{{key}}} {' '.join(x for x in parts if x)}" + "\n"
                         rf"\par{{\footnotesize\color{{tuftegray}}cited in {back}}}")
        return ("\\clearpage\n\\phantomsection\\addcontentsline{toc}{section}{Bibliography}\n"
                "\\renewcommand{\\refname}{Bibliography}\n\\begin{thebibliography}{99}\n" + "\n".join(items)
                + "\n\\end{thebibliography}")

    def link_back(self, i, name):
        return (rf"\hyperref[{self.lab[i]}]{{{self.label(name)}}}" if i.startswith("c-")
                else rf"\hyperlink{{{i}}}{{{tex_esc(name)}}}")

    def index(self):
        d, rows = self.d, []
        for name in sorted(d.defs, key=str.lower):
            c = d.defs[name][0]
            users = ", ".join(rf"\hyperref[{self.lab[u.id]}]{{\ref*{{{self.lab[u.id]}}}}}" for u in d.users(c))
            rows.append(rf"\hyperref[{self.lab[c.id]}]{{{self.label(name)}}} & \ref*{{{self.lab[c.id]}}} & {users} \\")
        return ("\\clearpage\n\\hypertarget{index}{}\\phantomsection\\addcontentsline{toc}{section}{Chunk index}\n"
                "\\section*{Chunk index}\n\\begin{longtable}{@{}p{0.40\\textwidth}rp{0.42\\textwidth}@{}}\n"
                "\\textbf{chunk} & \\textbf{defined} & \\textbf{used in} \\\\\\hline\n\\endhead\n" + "\n".join(rows)
                + "\n\\end{longtable}")

    def render(self):
        d = self.d
        style = self.P["Latex"](style="default").get_style_defs() if self.P else ""
        head = (PREAMBLE.replace("@@STYLE@@", style).replace("@@TITLE@@", tex_esc(d.title))
                .replace("@@PDFTITLE@@", tex_esc(d.title)).replace("@@SOURCE@@", tex_code_esc(d.src_name))
                .replace("@@NAMES@@", "\n".join(rf"\expandafter\gdef\csname cn@{self.lab[cs[0].id]}\endcsname{{{tex_code_esc(n)}}}"
                                                for n, cs in d.defs.items())))
        body, first_h1 = [], True
        for it in d.root.items:
            if it[0] == "head":
                if it[1] == 1 and first_h1:
                    first_h1 = False                       # the first level-1 heading is the title
                else:
                    body.append(self.heading(it))
            elif it[0] == "prose":
                body.append(self.prose(it[1]))
            elif it[0] == "code":
                body.append(self.code(it[1], d.root))
            elif it[0] == "chunk":
                body.append(self.chunk(it[1], 0))
            elif it[0] == "img":
                body.append(self.figure(it))
            elif it[0] == "hint":
                body.append(self.hint(it))
        body += [self.index(), self.bibliography(), "\\end{document}"]
        return head + "\n\n".join(b for b in body if b) + "\n"


# ============================================================ HTML stylesheet
CSS = r"""
:root{--fg:#111;--muted:#6f6f66;--rule:#cfcfc2;--code:transparent;--accent:#8a2a2a;--hl:#f4eecb;--paper:#fffff8;--bad:#a00;--hint-bg:transparent}
@media (prefers-color-scheme:dark){:root{--fg:#dedcd2;--muted:#9a988c;--rule:#45443d;--accent:#d99a8a;--hl:#3b3820;--paper:#151513;--bad:#f08a72}}
*{box-sizing:border-box}
::selection{background:var(--hl)}
html{font-size:16px}
body{margin:0;font:1.1rem/1.55 et-book,"Palatino Linotype","Book Antiqua",Palatino,"TeX Gyre Pagella",Georgia,serif;color:var(--fg);background:var(--paper);
 text-rendering:optimizeLegibility;font-variant-numeric:oldstyle-nums}
main{max-width:41rem;margin:0 auto;padding:3rem 1.4rem 6rem}
a{color:inherit;text-decoration:underline;text-decoration-thickness:.05em;text-decoration-color:var(--muted);text-underline-offset:.18em}
a:hover{color:var(--accent);text-decoration-color:var(--accent)}
h1,h2,h3{font-family:inherit;font-weight:400;font-style:italic;line-height:1.15;color:var(--fg)}
figure.fig{margin:1.6rem 0;text-align:center;color:var(--fg)}figure.fig svg,figure.fig img{max-width:100%;height:auto}
figcaption{color:var(--muted);font-size:.9rem;margin-top:.4rem}
h1.title{font-style:normal;font-size:2.6rem;margin:.4rem 0 .3rem}.sub{color:var(--muted);font-style:italic;font-size:1.2rem;margin-top:0}
h2{font-size:1.8rem;margin:3.2rem 0 1rem;border:0}h3{font-size:1.4rem;margin:2.2rem 0 .8rem}
code,pre,.fname{font-family:"Latin Modern Mono",ui-monospace,"DejaVu Sans Mono",Menlo,Consolas,monospace}
code{background:none;padding:0;font-size:.86em}
.display{margin:.8rem 0;overflow-x:auto}
.cite a{font-size:.92em}.cite{white-space:nowrap}
.chunk{margin:2.2rem 0;padding:0}
.chunk.nested{margin:1.4rem 0 1.4rem .9rem;padding-left:.9rem;border-left:1px solid var(--rule)}
.chunk:target>.chunk-head{background:var(--hl)}
.chunk-head{font-size:1.02rem;padding:.1rem .3rem;margin-left:-.3rem}
.chunk-head .num{color:var(--muted);margin-right:.4rem}
.cname{font-style:normal;font-family:"Latin Modern Mono",ui-monospace,"DejaVu Sans Mono",monospace;font-size:.88em}.eq{color:var(--muted)}
.badge{font-variant:small-caps;letter-spacing:.06em;color:var(--muted);font-size:.9rem;font-weight:400}
.fname{font-weight:400}
.meta{font-style:italic;font-size:.82rem;line-height:1.4;color:var(--muted);margin:.1rem 0 .6rem}
pre.code{background:none;border:0;border-left:1px solid var(--rule);border-radius:0;padding:.15rem 0 .15rem 1rem;white-space:pre-wrap;overflow-wrap:anywhere;
 font-size:.8rem;line-height:1.45;margin:.9rem 0;tab-size:4}
a.ref{font-weight:400}.ref.bad{color:var(--bad)}.rn{color:var(--muted);font-size:.8em;margin-left:.15rem}
.hint{border-left:2px solid var(--accent);padding:.2rem .9rem;font-style:italic;margin:1rem 0}
table.index{border-collapse:collapse;width:100%;font-size:.9rem;border-top:2px solid var(--fg);border-bottom:2px solid var(--fg)}
.index th{border-bottom:1px solid var(--fg);padding:.3rem .5rem;text-align:left;font-weight:400;font-style:italic}
.index td{padding:.2rem .5rem;text-align:left;vertical-align:top}
.index td.n{white-space:nowrap;color:var(--muted)}
ol.bib{padding-left:2.2rem;font-size:.95rem}ol.bib li{margin:.5rem 0}
nav ul{list-style:none;padding:0;margin:0}
nav#contents{border-top:1px solid var(--rule);border-bottom:1px solid var(--rule);padding:.5rem 0;margin:1.6rem 0;font-size:.95rem}
nav#contents h2{border:0;margin:.4rem 0;font-size:1.3rem}nav#contents h3{margin:.8rem 0 .2rem;font-size:1rem}
nav li.l2{margin-left:.9rem}nav li{margin:.12rem 0}
nav#side{display:none}
@media screen and (min-width:1180px){
 nav#side{display:block;position:fixed;top:0;left:0;bottom:0;width:17rem;overflow-y:auto;padding:1.6rem 1.1rem;
  border-right:1px solid var(--rule);font-size:.88rem;line-height:1.4}
 nav#side a{text-decoration:none}nav#side a:hover{text-decoration:underline}
 nav#side .st{font-style:italic;margin:.7rem 0 .3rem;color:var(--muted)}nav#side li.l2{margin-left:.7rem}
 main{margin-left:19rem;margin-right:0;max-width:44rem;padding-left:2.5rem;padding-right:2.5rem}nav#contents{display:none}}
@media screen and (min-width:1340px){
 main{max-width:none;padding-right:3rem}
 main>*{max-width:38rem}
 .chunk{position:relative}
 .meta{float:right;clear:right;width:14.5rem;margin:.2rem -16.5rem .6rem 0;font-size:.8rem}
 .chunk.nested .meta{margin-right:-17.4rem}.chunk.nested .chunk.nested .meta{margin-right:-18.3rem}}
@media print{body{background:#fff}nav#side{display:none}main{max-width:none;margin:0;padding:0}
 pre.code{break-inside:avoid;white-space:pre-wrap}h1,h2,h3,.chunk-head,.meta{break-after:avoid}}
"""


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("source")
    ap.add_argument("-o", "--outdir", default=".")
    ap.add_argument("--highlight", action="store_true", help="colour code with pygments (needs pygments; the files get harder to read)")
    ap.add_argument("--only", choices=["html", "tex"], help="write just one of the two outputs")
    ap.add_argument("--mathjax", default=MATHJAX, help="URL of the MathJax script used by the HTML (default: jsDelivr CDN)")
    a = ap.parse_args()
    global HIGHLIGHT
    HIGHLIGHT = a.highlight
    src = Source.read(a.source)
    doc = Doc(src.parse(), os.path.basename(a.source), src.base)
    os.makedirs(a.outdir, exist_ok=True)
    stem = os.path.join(a.outdir, os.path.splitext(os.path.basename(a.source))[0])
    if a.only != "tex":
        open(stem + ".html", "w", encoding="utf-8").write(HtmlWeaver(doc, a.mathjax).render())
        print(f"wrote {stem}.html  ({len(doc.defs)} chunks, {len(doc.cite_no)} citations)")
    if a.only != "html":
        open(stem + ".tex", "w", encoding="utf-8").write(TexWeaver(doc, a.outdir).render())
        print(f"wrote {stem}.tex   -> compile with:  xelatex {os.path.basename(stem)}.tex   (twice; pdflatex cannot read all of Unicode)")


if __name__ == "__main__":
    main()
