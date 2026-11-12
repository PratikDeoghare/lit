#!/usr/bin/env python3
"""Tangle a brashtag literate source into plain source files.

    python tangle.py llm-rig.lit [outdir] [--no-map]

Needs the brashtag parser:  pip install git+https://github.com/PratikDeoghare/brashtag.git

Conventions (see llm-rig.lit):
  #name{ ... }        a bag; any tag other than h1..h6, cite, bibliography defines a chunk.
                      Chunks may nest; chunk names are global (spaces allowed).
  ```...```           a fenced code Code (3+ backticks) belongs to its innermost chunk.
                      The text is dedented by the indentation of the closing fence.
                      One or two backticks are inline code and are prose.
  <<<name>>>          alone on a line inside code: expands chunk `name`, keeping indentation.
  \\<<<name>>>        the same line with a backslash first is literal text: tangle writes <<<name>>>.
                      (A line that already starts with backslashes loses one.)
  #img{`pic.svg` caption}  a figure for the woven output; not a chunk (see weave.py).
  #include{chapter.lit}  replaced by the text of that file (relative to this one); lets a program span files.
  chunk names ending in an extension (merge.go, rig/model.py) are written out as files.

Alongside the files it writes <outdir>/<source>.map, a sourcemap from generated lines back to
the .lit (see sourcemap.py; absorb.py uses it to fold edits of generated files back in).

The document is parsed inside an implicit root bag, because brashtag.parse() reads only
the first bag of its input and would silently ignore the rest.
"""
import os
import re
import sys

try:
    import brashtag
    from brashtag import Bag, Blob, Code
    from brashtag.parser import Error
except ImportError:
    try:                                   # bundled copy (MIT), next to this script
        import _brashtag as brashtag
        from _brashtag import Bag, Blob, Code, Error
    except ImportError:
        sys.exit("needs the brashtag parser:\n  pip install git+https://github.com/PratikDeoghare/brashtag.git")

HEADINGS = {f"h{i}" for i in range(1, 7)}
HINT = "agent-fill-in"
RESERVED = HEADINGS | {"cite", "bibliography", "include", "img", HINT}
REF = re.compile(r"^(\s*)<<<(.+?)>>>\s*$")
ESCAPED = re.compile(r"^(\s*)\\(\\*<<<.+?>>>\s*)$")      # \<<<name>>> alone on a line: a literal line
FILE = re.compile(r"\.\w+$")


class Source:
    """A literate program made of one or more files, seen as one text.

    `#include{chapter.lit}` anywhere in a file is replaced by the text of that file (relative to the
    including file), so every tool works on one flat text, `self.text`, and `self.pieces` remembers
    which stretch of it came from which file: (flat start, flat end, file, offset in that file).
    Edits to the flat text are carried back to the files by `write_back`.
    """

    def __init__(self, root_path):
        self.root = os.path.normpath(root_path)
        self.base = os.path.dirname(self.root)
        self.texts, self.pieces, self.order, self.text = {}, [], [], ""

    @classmethod
    def read(cls, path):
        self = cls(path)
        parts = []
        self._expand(self.root, (), parts)
        pos = 0
        for chunk, name, ofs in parts:
            self.pieces.append((pos, pos + len(chunk), name, ofs))
            pos += len(chunk)
        self.text = "".join(c for c, _, _ in parts)
        return self

    def rel(self, path):
        return os.path.relpath(path, self.base or ".").replace(os.sep, "/")

    def _read_file(self, path, why):
        try:
            with open(path, encoding="utf-8", newline="") as f:
                text = f.read()
        except OSError as e:
            sys.exit(f"{why}: cannot read {path}: {e.strerror or e}")
        if "\r" in text:
            sys.exit(f"{path}: carriage returns found; convert it to LF line endings "
                     "(the sourcemap uses character offsets)")
        return text

    def _expand(self, path, stack, parts):
        name = self.rel(path)
        if path in stack:
            sys.exit("recursive #include: " + " -> ".join(self.rel(p) for p in stack + (path,)))
        if name in self.texts:
            sys.exit(f"{name} is included twice; each chapter file may appear once "
                     "(use <<<chunk>>> references to share code)")
        text = self._read_file(path, stack and self.rel(stack[-1]) or name)
        self.texts[name] = text
        self.order.append(name)
        at = 0
        for start, end, target, line in _includes(text, name):
            parts.append((text[at:start], name, at))
            sub = os.path.normpath(os.path.join(os.path.dirname(path), target))
            if not os.path.exists(sub):
                sys.exit(f"{name}:{line}: #include{{{target}}}: no such file")
            self._expand(sub, stack + (path,), parts)
            at = end
        parts.append((text[at:], name, at))

    # -- positions
    def _piece(self, off):
        for p in self.pieces:
            if p[0] <= off < p[1]:
                return p
        return self.pieces[-1] if self.pieces else (0, 0, self.rel(self.root), 0)

    def locate(self, off):
        """flat offset -> (file, line, column), both 1-based."""
        s, _, name, ofs = self._piece(off)
        at = ofs + (off - s)
        text = self.texts.get(name, "")
        line = text.count("\n", 0, at) + 1
        return name, line, at - (text.rfind("\n", 0, at) + 1) + 1

    def where(self, off):
        name, line, _ = self.locate(off)
        return f"{name}:{line}"

    def flat_offset(self, name, line, col=1):
        """(file, line, column) -> flat offset, or None if that spot is not part of the flat text."""
        text = self.texts[name]
        at = sum(len(l) + 1 for l in text.split("\n")[:line - 1]) + col - 1
        for s, e, n, ofs in self.pieces:
            if n == name and ofs <= at < ofs + (e - s):
                return s + at - ofs
        return None

    def parse(self):
        return parse_text(self.text, self.where)

    # -- writing back
    def split_back(self, new_text):
        """{file: new text} for every file that changes when the flat text becomes `new_text`."""
        import difflib
        old = self.text
        if new_text == old:
            return {}
        la, lb = old.splitlines(True), new_text.splitlines(True)
        ca, cb = [0], [0]
        for l in la:
            ca.append(ca[-1] + len(l))
        for l in lb:
            cb.append(cb[-1] + len(l))
        edits = []                                         # (old start, old end, replacement)
        for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, la, lb, autojunk=False).get_opcodes():
            if tag == "equal":
                continue
            a0, a1, rep = ca[i1], ca[i2], new_text[cb[j1]:cb[j2]]
            if any(a0 < p[0] < a1 for p in self.pieces):   # a change that straddles two files: go finer
                for t2, k1, k2, m1, m2 in difflib.SequenceMatcher(None, old[a0:a1], rep, autojunk=False).get_opcodes():
                    if t2 != "equal":
                        edits.append((a0 + k1, a0 + k2, rep[m1:m2]))
            else:
                edits.append((a0, a1, rep))
        out = {n: [] for n in self.texts}
        for a0, a1, rep in edits:
            if a0 == a1:                                   # an insertion belongs to the file that ends there
                cand = [p for p in self.pieces if p[0] < a0 <= p[1]] or [p for p in self.pieces if p[0] == a0]
                s, _, name, ofs = cand[0]
                out[name].append((ofs + a0 - s, ofs + a0 - s, rep))
                continue
            first = True
            for s, e, name, ofs in self.pieces:
                lo, hi = max(a0, s), min(a1, e)
                if lo < hi:
                    out[name].append((ofs + lo - s, ofs + hi - s, rep if first else ""))
                    first = False
        new = {}
        for name, eds in out.items():
            if not eds:
                continue
            text, shift = self.texts[name], 0
            for a0, a1, rep in sorted(eds):
                text = text[:a0 + shift] + rep + text[a1 + shift:]
                shift += len(rep) - (a1 - a0)
            new[name] = text
        check = Source.__new__(Source)
        check.__dict__.update(root=self.root, base=self.base)
        check.texts, check.pieces, check.order = {}, [], []
        parts = []
        check._read_file = lambda path, why: (new if self.rel(path) in new else self.texts).get(self.rel(path)) \
            or self._read_file(path, why)
        check._expand(self.root, (), parts)
        if "".join(c for c, _, _ in parts) != new_text:
            sys.exit("the edit cannot be carried back to the chapter files (it adds, removes or moves an "
                     "#include); change the #include lines in the files themselves")
        return new

    def write_back(self, new_text):
        """Write the files that change when the flat text becomes `new_text`; -> {file: (old, new)}."""
        changed = self.split_back(new_text)
        for name, text in changed.items():
            path = os.path.join(self.base, name)
            tmp = path + ".lit.tmp"
            with open(tmp, "w", encoding="utf-8", newline="") as f:
                f.write(text)
            os.replace(tmp, path)
        done = {n: (self.texts[n], t) for n, t in changed.items()}
        for n, t in changed.items():
            self.texts[n] = t
        self.text = new_text
        self.pieces = Source.read(self.root).pieces
        return done


def _includes(text, name):
    """[(start, end, path, line)] of the #include{path} bags in one file (not looking inside them)."""
    root = parse_text(text, lambda off: f"{name}:{text.count(chr(10), 0, off) + 1}")
    found, pos = [], 0

    def go(bag):
        nonlocal pos
        for k in bag.kids:
            if isinstance(k, Blob):
                pos += len(k.text)
            elif isinstance(k, Code):
                pos += 2 * len(k.tag) + len(k.text)
            else:
                start = pos
                pos += len(k.tag) + 2
                go(k)
                pos += 1
                if k.tag == "include":
                    if not all(isinstance(x, Blob) for x in k.kids) or not "".join(x.text for x in k.kids).strip():
                        sys.exit(f"{name}:{text.count(chr(10), 0, start) + 1}: #include{{}} wants a file name")
                    found.append((start, pos, "".join(x.text for x in k.kids).strip(),
                                  text.count("\n", 0, start) + 1))

    go(root)
    return found


def parse_text(text, where):
    """Parse literate text into a root Bag; exits with file:line on malformed input.
    `where(offset)` names a position in the text for messages."""
    head = "#document{"
    rest, root = brashtag.parse_bag(head + text + "}")
    at = lambda r: max(0, len(head) + len(text) + 1 - len(r) - len(head))
    if isinstance(root, Error):
        sys.exit(f"{where(at(rest) if rest else len(text))}: {root.s}")
    if rest.strip():
        off = at(rest) - 1
        sys.exit(f"{where(off)}: a stray '}}' in prose closed the document early, near: "
                 f"{text[off:off + 50].strip()!r}  (a literal '}}' or '#' outside backticks is not allowed)")

    def check(bag):
        if "\n" in bag.tag or "#" in bag.tag or bag.tag != bag.tag.lstrip() or len(bag.tag) > 120:
            i = text.find("#" + bag.tag[:30])
            sys.exit(f"{where(max(i, 0))}: suspicious bag tag {bag.tag[:40]!r}...: a stray '#' in prose? (use backticks)")
        for k in bag.kids:
            if isinstance(k, Bag):
                check(k)
    check(root)
    return root


def parse_flat(text):
    """Parse program text that is already spliced together (what Source.text holds)."""
    return parse_text(text, lambda off: f"program text line {text.count(chr(10), 0, off) + 1}")


def load(path):
    """Parse a .lit file (and the chapter files it includes) into a root Bag."""
    return Source.read(path).parse()


def is_fence(code):
    return len(code.tag) >= 3


def fence_text(code):
    """Lines of a fenced block: drop the newline after the opening fence, and dedent
    by the whitespace that precedes the closing fence."""
    lines = code.text.split("\n")
    if lines and lines[0].strip() == "":
        lines = lines[1:]
    indent = ""
    if lines and lines[-1].strip() == "":
        indent, lines = lines[-1], lines[:-1]
    return [l[len(indent):] if l.startswith(indent) else l.lstrip() for l in lines]


def collect(root, holes=False):
    """name -> code lines (all fenced blocks of every definition, in document order).

    An #agent-fill-in{...} hint inside a chunk is left out, or with holes=True replaced by a marker line
    "\\x00hole:N" (N counts the hints in document order) so that a tool can find the gap in the expanded text."""
    chunks = {}
    count = [0]

    def visit(bag, depth):
        name = bag.tag.strip()
        if bag.tag == HINT:
            if depth and holes:
                chunks[stack[-1]].append("\x00hole:%d" % count[0])
            count[0] += depth > 0
            return
        if name in RESERVED:
            return
        lines = chunks.setdefault(name, [])
        stack.append(name)
        for k in bag.kids:
            if isinstance(k, Code) and is_fence(k):
                lines += fence_text(k)
            elif isinstance(k, Bag):
                visit(k, depth + 1)
        stack.pop()

    stack = []
    for k in root.kids:
        if isinstance(k, Code) and is_fence(k):
            print("warning: code outside any chunk is ignored", file=sys.stderr)
        elif isinstance(k, Bag):
            visit(k, 0)
    return chunks


def count_hints(root):
    """How many #agent-fill-in hints stand inside chunks, in document order."""
    return sum(1 for ln in sum(collect(root, True).values(), []) if ln.startswith("\x00hole:"))


def unescape(line):
    """The text tangle writes for a code line: an escaped reference line loses its first backslash."""
    m = ESCAPED.match(line)
    return m.group(1) + m.group(2) if m else line


def expand(name, chunks, trail=()):
    if name not in chunks:
        sys.exit(f"undefined chunk <<<{name}>>>")
    if name in trail:
        sys.exit("recursive chunk: " + " -> ".join(trail + (name,)))
    out = []
    for line in chunks[name]:
        m = REF.match(line)
        if m:
            sub = expand(m.group(2).strip(), chunks, trail + (name,))
            out += [m.group(1) + s if s else s for s in sub]
        else:
            out.append(unescape(line))
    return out


def _protect(src, outdir, name, path, new_text):
    """Keep a copy of edits to a generated file that are about to be overwritten (see litsync.py)."""
    try:
        import litsync
    except ImportError:
        return
    litsync.protect(src, outdir, name, path, new_text)


def main():
    argv = [a for a in sys.argv[1:] if a != "--no-map"]
    if not argv:
        sys.exit(__doc__)
    src, outdir = argv[0], (argv[1] if len(argv) > 1 else ".")
    root = load(src)
    chunks = collect(root)
    open_hints = count_hints(root)
    if open_hints:
        print(f"note: {open_hints} #agent-fill-in hint(s) still open; the generated files lack them (lit fill / fill.py fills them)", file=sys.stderr)
    used = {m.group(2).strip() for lines in chunks.values() for l in lines if (m := REF.match(l))}
    for name in chunks:
        if not FILE.search(name) and name not in used and chunks[name]:
            print(f"warning: chunk '{name}' is never used", file=sys.stderr)
    for name in chunks:
        if FILE.search(name):
            path = os.path.join(outdir, name)
            os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
            body = "\n".join(expand(name, chunks))
            _protect(src, outdir, name, path, body + "\n" if body else "")
            with open(path, "w", encoding="utf-8") as f:
                f.write(body + "\n" if body else "")
            print("wrote", path)
    if "--no-map" not in sys.argv:
        try:
            import sourcemap
            print("wrote", sourcemap.write(src, outdir, chunks))
        except ImportError:
            print("note: sourcemap.py not found; no sourcemap written", file=sys.stderr)
        except sourcemap.MapError as e:
            print(f"note: no sourcemap written: {e}", file=sys.stderr)


if __name__ == "__main__":
    main()
