#!/usr/bin/env python3
"""Let an assistant fill the gaps a literate program leaves for it.

    python fill.py prog.lit --list
    python fill.py prog.lit --dry-run [--only N]          # print the prompt for hint N, call nobody
    python fill.py prog.lit --agent anthropic --check 'go vet ./...'
    python fill.py prog.lit --agent 'claude -p'

A gap is a bag with the reserved tag `agent-fill-in`, written inside a chunk:

    #read the words{
      #agent-fill-in{count the words of standard input into counts}
    }

Tangle leaves the gap out.  For each gap this script gathers the context the way a person would read it
(the headings above it, the prose of its section, the chunk with the gap marked, the chunks that use it up
to the files, the chunks it uses or the hint names, the tangled file around it), asks an agent for the code,
checks the answer, writes it into the .lit (keeping the hint's words as prose) and records it in
PROG.lit.fills.jsonl for review.py.

Agents:  --agent anthropic[:MODEL]   the Anthropic Messages API (ANTHROPIC_API_KEY, optional ANTHROPIC_BASE_URL)
         --agent 'COMMAND'           a shell command that reads the prompt on stdin and prints the reply
         default: $LIT_AGENT, else the API when ANTHROPIC_API_KEY is set.

Checks before anything is written: the program must still parse and tangle, generated .py/.json/.go files
must be syntactically valid (.go only when `gofmt` is installed), the code must not hold placeholders, and
--check 'CMD' (run in a scratch copy of the tangled program) must pass.  A fill that fails is retried once
with the error.
"""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from tangle import HINT, RESERVED, FILE, REF, Bag, Blob, Code, Source, collect, expand, is_fence, fence_text  # noqa: E402

HOLE = re.compile(r"^(\s*)\x00hole:(\d+)$")
FENCE_OPEN = re.compile(r"^\s*(`{3,})[^`]*$")
CANNOT = re.compile(r"^\s*CANNOT:\s*(.*)$", re.M)
PLACEHOLDER = re.compile(r"\b(TODO|FIXME|XXX)\b|not implemented|unimplemented|implement me|your code here|\.\.\.\s*$|^\s*pass\s*(#.*)?$", re.I)

SYSTEM = """You fill in one gap in a literate program written in brashtag notation.

The program is prose interleaved with named code chunks. A line of code that consists only of <<<name>>> expands to the code of the chunk of that name, indented as the reference is; a line written \\<<<name>>> is literal text. Chunks are concatenated into source files by a tool called tangle. A gap is marked by the author with an agent-fill-in hint; you write the code lines that belong there.

Rules:
- Reply with exactly one fenced code block holding the lines to insert, and nothing else. Use a fence of three backticks (more if your code contains three).
- The lines are inserted verbatim as code lines of the chunk at the marked place. Keep the indentation that the neighbouring lines of the chunk call for; do not add indentation for the place where the chunk will be expanded (tangle adds that).
- Follow the language, naming, error handling and style of the surrounding code. Use only identifiers that exist in the context shown or in the language's standard library, and say nothing about imports unless the chunk is the place for them.
- Do not repeat code that is already there, do not leave placeholders, TODOs or ellipses, and do not write the code of other chunks. A <<<name>>> line is allowed only for a chunk that exists.
- If the hint cannot be satisfied from what you are shown, reply with a single line that starts with CANNOT: and gives the reason."""


# ---------------------------------------------------------------- the document as a flow of events

class Hint:
    def __init__(self, index, chunk, start, end, raw, file, line):
        self.index, self.chunk, self.start, self.end, self.raw, self.file, self.line = index, chunk, start, end, raw, file, line
        self.text = " ".join(raw.split())

    def describe(self):
        return "%s:%d  «%s»  %s" % (self.file, self.line, self.chunk, trunc(self.text or "(no description)", 80))


class Chunk:
    def __init__(self, name, parent):
        self.name, self.parent, self.items = name, parent, []   # items: ("prose", text) ("code", lines) ("hint", Hint) ("chunk", Chunk)


class Doc:
    """What the context builder needs: hints with offsets, chunks with their items, headings and prose in order."""

    def __init__(self, src, root):
        self.src, self.hints, self.defs, self.events = src, [], {}, []
        self.top = Chunk("", None)
        self._walk(root, 0, [], self.top, root=True)
        self.uses = {}
        for name, defs in self.defs.items():
            for c in defs:
                for it in c.items:
                    if it[0] == "code":
                        for l in it[1]:
                            m = REF.match(l)
                            if m:
                                self.uses.setdefault(m.group(2).strip(), [])
                                if c not in self.uses[m.group(2).strip()]:
                                    self.uses[m.group(2).strip()].append(c)

    def _walk(self, bag, pos, chain, chunk, root=False):
        """Walk a bag whose text starts at offset pos of the program text; returns the offset after it."""
        pos += 0 if root else len(bag.tag) + 2
        for k in bag.kids:
            if isinstance(k, Blob):
                if k.text.strip():
                    chunk.items.append(("prose", k.text))
                    self.events.append(("prose", k.text, chunk))
                pos += len(k.text)
            elif isinstance(k, Code):
                if is_fence(k):
                    chunk.items.append(("code", fence_text(k)))
                else:
                    chunk.items.append(("prose", "`" + k.text + "`"))
                pos += 2 * len(k.tag) + len(k.text)
            else:
                start = pos
                name = k.tag.strip()
                if k.tag == HINT and chain and all(c.strip() not in RESERVED for c in chain):
                    raw = "".join(x.text if isinstance(x, (Blob, Code)) else str(x) for x in k.kids)
                    f, line, _ = self.src.locate(start)
                    h = Hint(len(self.hints), chunk.name, start, start + len(str(k)), raw, f, line)
                    self.hints.append(h)
                    chunk.items.append(("hint", h))
                    self.events.append(("hint", h, chunk))
                    pos += len(str(k))
                elif name in RESERVED:
                    if name[:1] == "h" and name[1:].isdigit():
                        self.events.append(("head", int(name[1:]), prose_of(k.kids)))
                    pos += len(str(k))
                else:
                    c = Chunk(name, chunk if chunk.name else None)
                    self.defs.setdefault(name, []).append(c)
                    chunk.items.append(("chunk", c))
                    pos = self._walk(k, pos, chain + [name], c)
                    continue
        return pos if root else pos + 1


def prose_of(kids):
    out = []
    for k in kids:
        if isinstance(k, Blob):
            out.append(k.text)
        elif isinstance(k, Code):
            out.append("`" + k.text + "`" if len(k.tag) < 3 else "")
        elif k.tag == "cite":
            continue
        else:
            out.append(prose_of(k.kids))
    return re.sub(r"\n{3,}", "\n\n", re.sub(r"[ \t]+\n", "\n", "".join(out))).strip()


def trunc(s, n):
    return s if len(s) <= n else s[:n - 1].rstrip() + "…"


def find_hints(src):
    root = src.parse()
    return Doc(src, root).hints


def window(lines, at, before, after):
    lo, hi = max(0, at - before), min(len(lines), at + after + 1)
    return "\n".join((["…"] if lo else []) + lines[lo:hi] + (["…"] if hi < len(lines) else []))


def indent_block(text, pad):
    return "\n".join(pad + l if l else l for l in text.split("\n"))


def chunk_prose(c):
    return "\n\n".join(prose_of([Blob(it[1])]) for it in c.items if it[0] == "prose" and it[1].strip()).strip()


def code_lines(c):
    return [l for it in c.items if it[0] == "code" for l in it[1]]


def chunk_text(c, mark):
    out = []
    for it in c.items:
        if it[0] == "prose" and it[1].strip():
            out.append(it[1].strip())
        elif it[0] == "code":
            out.append("\n".join(it[1]))
        elif it[0] == "hint":
            out.append(("⟦ FILL IN HERE: %s ⟧" if it[1] is mark else "⟦ another unfilled hint: %s ⟧") % it[1].text)
        elif it[0] == "chunk":
            out.append("(nested chunk «%s»)" % it[1].name)
    return "\n\n".join(out)


def use_chain(doc, name, max_depth=4, max_sites=8):
    out, seen, queue = [], set(), [(name, 1)]
    while queue and len(out) < max_sites:
        q, depth = queue.pop(0)
        for u in doc.uses.get(q, []):
            if id(u) in seen or len(out) >= max_sites:
                continue
            seen.add(id(u))
            line = next((i for i, l in enumerate(code_lines(u)) if (m := REF.match(l)) and m.group(2).strip() == q), -1)
            out.append((u, depth, line, q))
            if depth < max_depth and u.name and not FILE.search(u.name):
                queue.append((u.name, depth + 1))
    return out


# ---------------------------------------------------------------- the prompt

def build_prompt(src, k, budget=24000):
    root = src.parse()
    doc = Doc(src, root)
    if not 0 <= k < len(doc.hints):
        raise ValueError("no hint number %d (the program has %d)" % (k, len(doc.hints)))
    site = doc.hints[k]
    holder = next(c for c in sum(doc.defs.values(), []) if any(it[0] == "hint" and it[1] is site for it in c.items))
    heads, crumbs, section, found = {}, [], [], False
    for ev in doc.events:
        if ev[0] == "head":
            if found:
                break
            heads[ev[1]] = ev[2]
            heads = {l: t for l, t in heads.items() if l <= ev[1]}
            section = []
        elif ev[0] == "prose":
            section.append(ev[1])
        elif ev[0] == "hint" and ev[1] is site:
            found, crumbs = True, [heads[l] for l in sorted(heads) if heads[l]]
    chunks = collect(root, True)
    files = part_of(doc, holder.name)
    if FILE.search(holder.name):
        files = [holder.name] + files
    lang = os.path.splitext(files[0])[1].lstrip(".") if files else ""

    secs = []
    task = "Hint (%s line %d): %s\nChunk to fill: «%s»" % (site.file, site.line, site.text or "(none)", holder.name)
    task += ("\nEnds up in: " + ", ".join(files)) if files else "\nThis chunk is not expanded into any file yet."
    task += ("\nLanguage: " + lang) if lang else ""
    secs.append(("task", task, 0))
    if crumbs:
        secs.append(("where in the document", " › ".join(crumbs), 1))
    intro = []
    for ev in doc.events:
        if ev[0] == "head" and ev[1] >= 2 or ev[0] == "hint" or ev[0] == "prose" and ev[2].name:
            break
        if ev[0] == "prose":
            intro.append(ev[1].strip())
    if intro:
        secs.append(("what the program is", trunc(prose_of([Blob("\n\n".join(intro))]), 1800), 4))
    anc, p = [], holder.parent
    while p is not None and p.name:
        if chunk_prose(p):
            anc.insert(0, "«%s»: %s" % (p.name, trunc(chunk_prose(p), 800)))
        p = p.parent
    if anc:
        secs.append(("the chunks around this one", "\n\n".join(anc), 3))
    sp = prose_of([Blob("\n\n".join(t for t in section))])
    if sp:
        secs.append(("prose of this section", trunc(sp, 2500), 2))
    uses = []
    for u, depth, line, via in use_chain(doc, holder.name):
        who = "«%s»" % u.name if u.name else "the document"
        head = "%s«%s» is expanded in %s:" % ("  " * (depth - 1), via, who)
        if depth == 1 and chunk_prose(u):
            head += "\n(" + trunc(chunk_prose(u), 400) + ")"
        body = window(code_lines(u), line, 5, 5) if line >= 0 else ""
        uses.append(head + "\n" + indent_block(body, "  " * (depth - 1)))
    if uses:
        secs.append(("where this chunk is used", "\n\n".join(uses), 2))
    want = []

    def add_want(n):
        n = n.strip()
        if n in doc.defs and n != holder.name and n not in want:
            want.append(n)
    for l in code_lines(holder):
        m = REF.match(l)
        if m:
            add_want(m.group(2))
    for m in re.finditer("<<<([^>]+)>>>|«([^»]+)»|`([^`]+)`", site.text + " " + site.raw):
        add_want("".join(g or "" for g in m.groups()))
    refs = []
    for n in want:
        parts = []
        for c in doc.defs[n]:
            if chunk_prose(c):
                parts.append(trunc(chunk_prose(c), 300))
            parts.append(trunc("\n".join(code_lines(c)), 1200))
        refs.append("«%s»:\n%s" % (n, "\n".join(parts)))
    if refs:
        secs.append(("chunks this one expands or the hint mentions", "\n\n".join(refs), 3))
    secs.append(("the chunk, with the gap marked", chunk_text(holder, site), 1))
    if files and files[0] in chunks:
        lines = expand(files[0], chunks)
        at = next((i for i, l in enumerate(lines) if (m := HOLE.match(l)) and int(m.group(2)) == k), -1)
        if at >= 0:
            shown = []
            for l in lines:
                m = HOLE.match(l)
                shown.append(m.group(1) + ("⟦ FILL IN HERE ⟧" if int(m.group(2)) == k else "⟦ another unfilled hint ⟧") if m else l)
            secs.append(("the tangled file around the gap", "%s (line %d):\n%s" % (files[0], at + 1, window(shown, at, 12, 12)), 1))
    keep = sorted(range(len(secs)), key=lambda i: (secs[i][2], i))
    used, chosen = 0, set()
    for i in keep:
        cost = len(secs[i][1]) + len(secs[i][0]) + 8
        if used + cost <= budget or secs[i][2] == 0:
            chosen.add(i)
            used += cost
    shown = [secs[i] for i in range(len(secs)) if i in chosen]
    user = "\n\n".join("=== %s ===\n%s" % (t, b) for t, b, _ in shown)
    user += "\n\nWrite the code lines that replace the gap marked FILL IN HERE."
    return SYSTEM, user, [t for t, _, _ in shown], site


def part_of(doc, name):
    """The file chunks that, through any chain of references, expand the chunk."""
    out, seen, queue = [], {name}, [name]
    while queue:
        n = queue.pop(0)
        for u in doc.uses.get(n, []):
            if u.name and u.name not in seen:
                seen.add(u.name)
                if FILE.search(u.name):
                    out.append(u.name)
                else:
                    queue.append(u.name)
    return out


# ---------------------------------------------------------------- agents

def make_agent(spec, model=""):
    """-> (name, ask(system, user) -> reply text)"""
    spec = spec or os.environ.get("LIT_AGENT", "")
    if not spec and os.environ.get("ANTHROPIC_API_KEY"):
        spec = "anthropic"
    if not spec:
        sys.exit("fill: no agent. Use --agent anthropic (with ANTHROPIC_API_KEY set) or --agent 'COMMAND', or set LIT_AGENT.")
    if spec == "anthropic" or spec.startswith("anthropic:"):
        key = os.environ.get("ANTHROPIC_API_KEY")
        if not key:
            sys.exit("fill: ANTHROPIC_API_KEY is not set")
        mdl = model or spec.partition(":")[2] or os.environ.get("LIT_MODEL", "") or "claude-sonnet-4-5"
        base = os.environ.get("ANTHROPIC_BASE_URL", "https://api.anthropic.com").rstrip("/")

        def ask(system, user, timeout=300):
            body = json.dumps({"model": mdl, "max_tokens": 4096, "system": system,
                               "messages": [{"role": "user", "content": user}]}).encode()
            req = urllib.request.Request(base + "/v1/messages", body, {
                "content-type": "application/json", "x-api-key": key, "anthropic-version": "2023-06-01"})
            try:
                with urllib.request.urlopen(req, timeout=timeout) as r:
                    data = json.load(r)
            except urllib.error.HTTPError as e:
                raise RuntimeError("the API answered %d: %s" % (e.code, e.read()[:300].decode("utf-8", "replace")))
            except OSError as e:
                raise RuntimeError("cannot reach the API: %s" % e)
            return "".join(b.get("text", "") for b in data.get("content", []) if b.get("type") == "text")
        return "anthropic:" + mdl, ask

    def ask(system, user, timeout=300):
        p = subprocess.run(spec, shell=True, input=system + "\n\n" + user, capture_output=True, text=True, timeout=timeout)
        if p.returncode:
            raise RuntimeError("the agent command failed (%d): %s" % (p.returncode, p.stderr.strip()[:300]))
        return p.stdout
    return "command: " + spec, ask


# ---------------------------------------------------------------- reading the reply, checking, inserting

def extract_code(reply):
    """-> (code lines, cannot reason) ; raises ValueError for an unusable reply."""
    lines = reply.replace("\r\n", "\n").split("\n")
    for i, l in enumerate(lines):
        m = FENCE_OPEN.match(l)
        if m:
            close = re.compile(r"^\s*`{%d,}\s*$" % len(m.group(1)))
            for j in range(i + 1, len(lines)):
                if close.match(lines[j]):
                    return trim_blank(lines[i + 1:j]), ""
            raise ValueError("the reply opens a code fence but never closes it")
    m = CANNOT.search(reply)
    if m and len(reply.strip()) == len(m.group(0).strip()):
        return None, m.group(1).strip()
    if not reply.strip():
        raise ValueError("the reply is empty")
    return trim_blank(lines), ""


def trim_blank(lines):
    while lines and not lines[0].strip():
        lines = lines[1:]
    while lines and not lines[-1].strip():
        lines = lines[:-1]
    return [l.rstrip(" \t\r") for l in lines]


def insertion(text, site, code, drop_hint=False):
    n = max([3] + [len(r) + 1 for l in code for r in re.findall("`+", l)])
    fence = "`" * n
    out = ""
    if site.start > 0 and text[site.start - 1] != "\n":
        out += "\n"
    if site.raw.strip() and not drop_hint:
        out += site.raw.strip() + "\n\n"
    out += fence + "\n" + "\n".join(code) + "\n" + fence
    if site.end < len(text) and text[site.end] != "\n":
        out += "\n"
    return out


def check_code(code):
    """Findings for the lines of one filled gap on their own: [(level, message)]"""
    if not "".join(code).strip():
        return [("error", "the filled code is empty")]
    out = [("error", "line %d looks like a placeholder: %s" % (i + 1, l.strip())) for i, l in enumerate(code) if PLACEHOLDER.search(l)]
    if any("\x00" in l for l in code):
        out.append(("error", "the code contains a NUL character"))
    if len(code) > 80:
        out.append(("warn", "%d lines for one gap; consider splitting it into named chunks with prose" % len(code)))
    if any(l.startswith("\t") for l in code) and any(l.startswith("  ") for l in code):
        out.append(("warn", "the code mixes tab and space indentation"))
    return out


def tangle_text(text, where="program text"):
    """-> {file: body}, parsing text as a program; SystemExit becomes ValueError."""
    from tangle import parse_text
    try:
        root = parse_text(text, lambda off: "%s line %d" % (where, text.count("\n", 0, off) + 1))
        chunks = collect(root)
        return {n: "\n".join(expand(n, chunks)) + "\n" for n in chunks if FILE.search(n)}
    except SystemExit as e:
        raise ValueError(str(e.code))


def check_program(text):
    """The checks that need no model: the program parses and tangles, generated files are valid."""
    try:
        files = tangle_text(text)
    except ValueError as e:
        return [("error", "the program no longer parses or tangles: %s" % e)]
    out = []
    for name, body in files.items():
        ext = os.path.splitext(name)[1].lower()
        try:
            if ext == ".py":
                compile(body, name, "exec")
            elif ext == ".json":
                json.loads(body)
            elif ext == ".go" and shutil.which("gofmt"):
                p = subprocess.run(["gofmt", "-e"], input=body, capture_output=True, text=True)
                if p.returncode:
                    out.append(("error", "%s: syntax error: %s" % (name, p.stderr.strip().split("\n")[0])))
        except SyntaxError as e:
            out.append(("error", "%s:%s: syntax error: %s" % (name, e.lineno, e.msg)))
        except ValueError as e:
            out.append(("error", "%s: invalid JSON: %s" % (name, e)))
        for i, l in enumerate(body.split("\n")):
            if "\x00hole" in l:
                out.append(("error", "%s:%d: a gap marker leaked into the generated file" % (name, i + 1)))
    return out


def run_check(text, command, timeout=300):
    """Tangle the program into a scratch directory and run `command` there; -> findings."""
    try:
        files = tangle_text(text)
    except ValueError as e:
        return [("error", str(e))]
    with tempfile.TemporaryDirectory() as d:
        for name, body in files.items():
            path = os.path.join(d, name)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as f:
                f.write(body)
        try:
            p = subprocess.run(command, shell=True, cwd=d, capture_output=True, text=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            return [("error", "--check %r timed out" % command)]
    if p.returncode:
        return [("error", "--check %r failed (exit %d):\n%s" % (command, p.returncode, trunc((p.stdout + p.stderr).strip(), 1500)))]
    return []


# ---------------------------------------------------------------- the record: PROG.lit.fills.jsonl

def fills_path(src):
    return os.path.join(src.base, os.path.basename(src.root) + ".fills.jsonl")


def append_event(src, ev):
    with open(fills_path(src), "a", encoding="utf-8") as f:
        f.write(json.dumps(ev, ensure_ascii=False) + "\n")


def load_fills(src):
    order, by_id = [], {}
    try:
        lines = open(fills_path(src), encoding="utf-8").read().split("\n")
    except OSError:
        return []
    for l in lines:
        try:
            ev = json.loads(l)
        except ValueError:
            continue
        if ev.get("event") == "fill":
            by_id[ev["id"]] = ev
            order.append(ev)
        elif ev.get("event") == "status" and ev.get("id") in by_id:
            f = by_id[ev["id"]]
            f["status"] = ev.get("status") or f["status"]
            f["verdict"] = ev.get("verdict") or f.get("verdict", "")
            f["notes"] = f.get("notes", []) + ev.get("notes", [])
    return order


def record_status(src, fid, status, verdict="", notes=()):
    append_event(src, {"event": "status", "id": fid, "status": status, "t": time.time(), "verdict": verdict, "notes": list(notes)})


# ---------------------------------------------------------------- filling

def fill_one(ask, agent_name, src, k, budget, retries, check, drop_hint, out=print):
    system, user0, _, site = build_prompt(src, k, budget)
    user, last, notes = user0, "", []
    for attempt in range(1, retries + 2):
        reply = ask(system, user)
        try:
            code, cannot = extract_code(reply)
        except ValueError as e:
            last = str(e)
            user = user0 + "\n\nYour previous answer could not be used: " + last + "\nFollow the reply format exactly."
            continue
        if cannot:
            raise RuntimeError("the agent could not do it: " + cannot)
        ins = insertion(src.text, site, code, drop_hint)
        new_text = src.text[:site.start] + ins + src.text[site.end:]
        fs = check_code(code) + check_program(new_text)
        if check and not any(l == "error" for l, _ in fs):
            fs += run_check(new_text, check)
        errors = [m for l, m in fs if l == "error"]
        if not errors:
            notes = [m for l, m in fs if l == "warn"]
            orig = src.text[site.start:site.end]
            src.write_back(new_text)
            fid = "f-" + hashlib.sha256(("%s\0%s\0%s\0%s" % (site.chunk, site.text, "\n".join(code), time.time_ns())).encode()).hexdigest()[:6]
            rec = {"event": "fill", "id": fid, "status": "pending", "t": time.time(), "chunk": site.chunk, "file": site.file,
                   "line": site.line, "hint": site.text, "orig": orig, "inserted": ins, "code": "\n".join(code),
                   "agent": agent_name, "attempts": attempt, "notes": notes}
            append_event(src, rec)
            return rec, notes
        last = "\n".join(errors)
        out("  attempt %d failed its checks:\n    %s" % (attempt, trunc(last, 600).replace("\n", "\n    ")))
        user = (user0 + "\n\nYour previous answer was rejected:\n" + last + "\n\nYour previous code was:\n```\n" +
                "\n".join(code) + "\n```\nWrite the corrected code for the gap.")
    raise RuntimeError("after %d attempt(s): %s" % (retries + 1, last.split("\n")[0]))


def main(argv=None):
    ap = argparse.ArgumentParser(prog="fill.py", description="Fill the #agent-fill-in{} hints of a literate program.")
    ap.add_argument("source")
    ap.add_argument("--list", action="store_true", help="show the hints and stop")
    ap.add_argument("--dry-run", action="store_true", help="print the prompt for hint --only (default 0) and stop")
    ap.add_argument("--show-context", action="store_true", help="with --dry-run: only the sections and the size")
    ap.add_argument("--agent", default="", help='"anthropic[:MODEL]" or a shell command; default $LIT_AGENT')
    ap.add_argument("--model", default="")
    ap.add_argument("--check", default="", help="a command to run on the tangled program when the last hint is filled")
    ap.add_argument("--check-each", action="store_true", help="run --check after every fill")
    ap.add_argument("--retries", type=int, default=1)
    ap.add_argument("--only", type=int, default=None, help="fill just this hint number")
    ap.add_argument("--chunk", default="", help="only hints inside this chunk")
    ap.add_argument("--limit", type=int, default=1 << 30)
    ap.add_argument("--budget", type=int, default=24000, help="size of the prompt in characters")
    ap.add_argument("--drop-hint", action="store_true", help="do not keep the hint's words as prose above the code")
    a = ap.parse_args(argv)

    src = Source.read(a.source)
    sites = find_hints(src)
    if a.list:
        print("no #agent-fill-in hints" if not sites else "\n".join("%3d  %s" % (s.index, s.describe()) for s in sites))
        return 0
    if not sites:
        print("no #agent-fill-in hints to fill")
        return 0
    if a.dry_run or a.show_context:
        k = a.only or 0
        try:
            system, user, titles, site = build_prompt(src, k, a.budget)
        except ValueError as e:
            print("fill: %s" % e, file=sys.stderr)
            return 2
        if a.show_context:
            print("hint %d: %s\nsections: %s\nprompt: %d characters" % (k, site.describe(), "; ".join(titles), len(user)))
        else:
            print("--- system ---\n%s\n--- prompt ---\n%s" % (system, user))
        return 0
    if a.only is not None and not 0 <= a.only < len(sites):
        print("fill: no hint number %d; fill.py --list shows them" % a.only, file=sys.stderr)
        return 2
    name, ask = make_agent(a.agent, a.model)
    filled = failed = 0
    skip, first = set(), a.only
    key = lambda s: (s.chunk, s.raw, s.line)
    while filled + failed < a.limit:
        src = Source.read(a.source)       # read again each time: what one fill adds is context for the next
        sites = find_hints(src)
        pick = next((s for s in sites if (first is None or s.index == first) and (not a.chunk or s.chunk == a.chunk) and key(s) not in skip), None)
        if pick is None:
            break
        first = None
        print("filling " + pick.describe())
        check = a.check if (a.check_each or len(sites) <= 1) else ""
        try:
            rec, notes = fill_one(ask, name, src, pick.index, a.budget, a.retries, check, a.drop_hint)
        except (RuntimeError, ValueError, OSError, subprocess.TimeoutExpired) as e:
            print("  not filled: %s" % e, file=sys.stderr)
            skip.add(key(pick))
            failed += 1
            if a.only is not None:
                break
            continue
        for n in notes:
            print("  warning: " + n)
        print("  filled as %s (%d line(s)); review.py shows it" % (rec["id"], rec["code"].count("\n") + 1))
        filled += 1
        if a.only is not None:
            break
    print("%d filled, %d failed, %d hint(s) still open" % (filled, failed, len(find_hints(Source.read(a.source)))))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
