/* ---------- literate core: load, tangle, sourcemap, absorb, three-way merge, weave ---------- */
const HEADING = /^h[1-6]$/;
const RESERVED = new Set(["h1", "h2", "h3", "h4", "h5", "h6", "cite", "bibliography", "agent-fill-in"]);
const REF = /^(\s*)<<<(.+?)>>>\s*$/;
const ESC = /^(\s*)\\(\\*<<<.+?>>>\s*)$/;
const FILE = /\.\w+$/;
const unescapeLine = (l) => { const m = ESC.exec(l); return m ? m[1] + m[2] : l; };
const lineAt = (text, i) => { let n = 1; for (let k = 0; k < i && k < text.length; k++) if (text.charCodeAt(k) === 10) n++; return n; };
const toLines = (t) => { if (t === "") return []; const a = t.split("\n"); if (a[a.length - 1] === "") a.pop(); return a; };

class LitError extends Error { constructor(msg, line) { super(msg); this.line = line; } }

const FRIENDLY = {
  "unexpected {": "a { in prose. Put it inside backticks.",
  "unopened bag missing { after #": "a # in prose. Put it inside backticks.",
  "bag not closed": "a #bag{ is never closed.",
  "code not closed": "a run of backticks is never closed.",
};

function load(text) {
  const full = "#document{" + text + "}";
  const r = parseBag(full);
  if (r.error) {
    const at = r.rest === undefined ? full.length : full.length - r.rest.length - 10;
    throw new LitError(FRIENDLY[r.error] || r.error, lineAt(text, Math.max(0, at)));
  }
  if (r.rest.trim() !== "") {
    const at = full.length - r.rest.length - 11;
    throw new LitError("a stray } closed the document early. Put it inside backticks.", lineAt(text, Math.max(0, at)));
  }
  return r.node;
}

function offsets(root) {
  let pos = 0; const out = new Map();
  (function go(bag) {
    for (const k of bag.kids) {
      if (k instanceof Blob) pos += k.text.length;
      else if (k instanceof Code) { const s = pos + k.tag.length; out.set(k, s); pos = s + k.text.length + k.tag.length; }
      else { pos += k.tag.length + 2; go(k); pos += 1; }
    }
  })(root);
  return out;
}

function layout(code, off) {
  const raw = code.text.split("\n"); const starts = []; let pos = off;
  for (const r of raw) { starts.push(pos); pos += r.length + 1; }
  let lo = 0, hi = raw.length, indent = "";
  if (raw[0].trim() === "") lo = 1;
  if (hi > lo && raw[hi - 1].trim() === "") { indent = raw[hi - 1]; hi--; }
  const lines = [];
  for (let i = lo; i < hi; i++) {
    const r = raw[i];
    lines.push({ text: r.startsWith(indent) ? r.slice(indent.length) : r.trimStart(), start: starts[i], end: starts[i] + r.length });
  }
  return { indent, lines, nlLast: hi < raw.length };
}

const isFence = (c) => c.tag.length >= 3;

function analyseDoc(text) {
  const root = load(text), offs = offsets(root);
  const blocks = [], chunks = new Map(), bags = [];
  const visit = (bag, parent) => {
    const name = bag.tag.trim();
    if (RESERVED.has(name)) return;
    const entry = { name, parent, bag, number: bags.length + 1 };
    bags.push(entry);
    if (!chunks.has(name)) chunks.set(name, []);
    const list = chunks.get(name);
    for (const k of bag.kids) {
      if (k instanceof Code && isFence(k)) {
        const b = layout(k, offs.get(k)); b.chunk = name; const bid = blocks.length; blocks.push(b);
        b.lines.forEach((l, li) => list.push({ text: l.text, bid, li }));
      } else if (k instanceof Bag) visit(k, name);
    }
  };
  for (const k of root.kids) if (k instanceof Bag) visit(k, null);
  return { root, text, blocks, chunks, bags };
}

function expand(name, chunks, trail = []) {
  if (!chunks.has(name)) throw new LitError(`undefined chunk <<<${name}>>>`, 0);
  if (trail.includes(name)) throw new LitError("recursive chunk: " + [...trail, name].join(" -> "), 0);
  const out = [];
  for (const l of chunks.get(name)) {
    const m = REF.exec(l.text);
    if (m) {
      for (const s of expand(m[2].trim(), chunks, [...trail, name]))
        out.push(s.text === "" ? s : { text: m[1] + s.text, bid: s.bid, li: s.li, pfx: m[1] + s.pfx });
    } else out.push({ text: unescapeLine(l.text), bid: l.bid, li: l.li, pfx: "" });
  }
  return out;
}

function tangle(text) {
  const doc = analyseDoc(text), files = {}, origins = {};
  for (const name of doc.chunks.keys()) if (FILE.test(name)) {
    const ex = expand(name, doc.chunks);
    files[name] = ex.map((l) => l.text); origins[name] = ex;
  }
  return { doc, files, origins };
}

/* ---------- diff ---------- */
function hunks(a, b) {
  let p = 0; while (p < a.length && p < b.length && a[p] === b[p]) p++;
  let q = 0; while (q < a.length - p && q < b.length - p && a[a.length - 1 - q] === b[b.length - 1 - q]) q++;
  const x = a.slice(p, a.length - q), y = b.slice(p, b.length - q), n = x.length, m = y.length;
  const dp = Array.from({ length: n + 1 }, () => new Int32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--)
    dp[i][j] = x[i] === y[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out = []; let i = 0, j = 0, cur = null;
  const open = () => { if (!cur) cur = { s: p + i, e: p + i, add: [] }; };
  const close = () => { if (cur) { cur.e = p + i; out.push(cur); cur = null; } };
  while (i < n || j < m) {
    if (i < n && j < m && x[i] === y[j]) { close(); i++; j++; }
    else if (j >= m || (i < n && dp[i + 1][j] >= dp[i][j + 1])) { open(); i++; }
    else { open(); cur.add.push(y[j]); j++; }
  }
  close();
  return out;
}

/* ---------- absorb: fold an edit of a generated file back into the .lit ---------- */
function absorb(text, fname, oldLines, newLines) {
  const t = tangle(text), org = t.origins[fname], blocks = t.doc.blocks;
  const ops = [], errors = [], notes = [];
  const litText = (o, s) => {
    if (!s.startsWith(o.pfx)) { errors.push(`"${s.trim()}" is indented less than the reference that contains it, so the .lit cannot express it.`); return null; }
    return blocks[o.bid].indent + s.slice(o.pfx.length);
  };
  const lineOfOrigin = (o) => blocks[o.bid].lines[o.li];
  const hasNext = (o) => o.li < blocks[o.bid].lines.length - 1 || blocks[o.bid].nlLast;
  const insertAfter = (idx, lines) => {
    if (idx >= 0) {
      const o = org[idx], l = lineOfOrigin(o), tx = lines.map((s) => litText(o, s));
      if (tx.includes(null)) return;
      if (hasNext(o)) ops.push({ start: l.end + 1, end: l.end + 1, text: tx.join("\n") + "\n", ord: 0 });
      else ops.push({ start: l.end, end: l.end, text: "\n" + tx.join("\n"), ord: 0 });
    } else {
      const o = org[0], l = lineOfOrigin(o), tx = lines.map((s) => litText(o, s));
      if (tx.includes(null)) return;
      ops.push({ start: l.start, end: l.start, text: tx.join("\n") + "\n", ord: 1 });
    }
  };
  for (const h of hunks(oldLines, newLines)) {
    const n = h.e - h.s, m = h.add.length;
    for (let k = 0; k < Math.min(n, m); k++) {
      const o = org[h.s + k], l = lineOfOrigin(o), tx = litText(o, h.add[k]);
      if (tx !== null) ops.push({ start: l.start, end: l.end, text: tx, ord: 2 });
    }
    if (m > n) insertAfter(h.s + n - 1, h.add.slice(n));
    for (let k = m; k < n; k++) {
      const o = org[h.s + k], l = lineOfOrigin(o);
      ops.push({ start: l.start, end: l.end + (hasNext(o) ? 1 : 0), text: "", ord: 2 });
    }
  }
  if (errors.length) return { errors, notes, text };
  ops.sort((a, b) => a.start - b.start || a.end - b.end || a.ord - b.ord);
  let out = "", cur = 0;
  for (const op of ops) {
    if (op.start < cur) return { errors: ["two edits overlap; edit fewer lines at once."], notes, text };
    out += text.slice(cur, op.start) + op.text; cur = op.end;
  }
  out += text.slice(cur);
  try {
    const back = tangle(out).files[fname];
    if (JSON.stringify(back) !== JSON.stringify(newLines))
      return { errors: ["a chunk used in several places was touched, so the patched .lit would regenerate something different from your file."], notes, text };
  } catch (e) { return { errors: [e.message], notes, text }; }
  return { errors, notes, text: out };
}

/* ---------- three-way merge in generated-file space ---------- */
function diff3(base, ours, theirs) {
  const H = (o, n) => hunks(o, n).map((h) => [h.s, h.e, h.add]);
  const items = [...H(base, ours).map(([s, e, n]) => [s, e, n, "o"]), ...H(base, theirs).map(([s, e, n]) => [s, e, n, "t"])]
    .sort((a, b) => a[0] - b[0] || a[1] - b[1] || (a[3] < b[3] ? -1 : 1));
  const groups = [];
  for (const it of items) {
    const g = groups[groups.length - 1];
    const overlap = g && ((it[0] === it[1] && it[0] === g.s && g.s === g.e) || (it[0] < g.e && g.s < it[1]));
    if (overlap) { g.s = Math.min(g.s, it[0]); g.e = Math.max(g.e, it[1]); g.h.push(it); }
    else groups.push({ s: it[0], e: it[1], h: [it] });
  }
  return groups.map((g) => {
    const side = (who) => { let out = [], pos = g.s;
      for (const [hs, he, nw, w] of [...g.h].sort((a, b) => a[0] - b[0] || a[1] - b[1])) if (w === who) { out = out.concat(base.slice(pos, hs), nw); pos = he; }
      return out.concat(base.slice(pos, g.e)); };
    const hasO = g.h.some((h) => h[3] === "o"), hasT = g.h.some((h) => h[3] === "t");
    const lit = side("o"), gen = side("t");
    const kind = !hasT ? "lit" : !hasO ? "gen" : JSON.stringify(lit) === JSON.stringify(gen) ? "same" : "conflict";
    return { s: g.s, e: g.e, base: base.slice(g.s, g.e), lit, gen, kind };
  });
}

function outcome(r, act) {
  if (!act) act = r.kind === "conflict" ? "leave" : "apply";
  if (act === "apply") { const x = r.kind === "gen" ? r.gen : r.lit; return [x, x, x]; }
  if (act === "leave") return [r.lit, r.gen, r.base];
  if (act === "lit") return [r.lit, r.lit, r.lit];
  if (act === "gen") return [r.gen, r.gen, r.gen];
  if (act === "both") { const b = r.lit.concat(r.gen); return [b, b, b]; }
  throw new Error("unknown decision " + act);
}

// one generated file: -> {regions, newLit, gen, base, notes, errors}
function mergeFile(litText, fname, baseLines, genLines, decisions = {}) {
  const ours = tangle(litText).files[fname];
  const regions = diff3(baseLines, ours, genLines);
  let L = [], G = [], B = [], pos = 0;
  regions.forEach((r, i) => {
    const [l, g, b] = outcome(r, decisions[i]);
    const stable = baseLines.slice(pos, r.s);
    L = L.concat(stable, l); G = G.concat(stable, g); B = B.concat(stable, b); pos = r.e;
  });
  const rest = baseLines.slice(pos);
  L = L.concat(rest); G = G.concat(rest); B = B.concat(rest);
  let newLit = litText, errors = [], notes = [];
  if (JSON.stringify(L) !== JSON.stringify(ours)) {
    const a = absorb(litText, fname, ours, L);
    errors = a.errors; notes = a.notes; if (!errors.length) newLit = a.text;
  }
  return { regions, ours, newLit, gen: G, base: B, errors, notes };
}

/* ---------- weave: a compact version of weave.py for the page ---------- */
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
const slug = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");

function bibEntries(bag) {
  const out = [];
  for (const k of bag.kids) if (k instanceof Bag) {
    const src = k.kids.filter((x) => x instanceof Code).map((x) => x.text).join("\n");
    const f = {}; for (const m of src.matchAll(/(\w+)\s*=\s*\{((?:[^{}]|\{[^{}]*\})*)\}/g)) f[m[1].toLowerCase()] = m[2].replace(/[{}]/g, "");
    out.push({ key: k.tag.trim(), f });
  }
  return out;
}

function weave(text) {
  const doc = analyseDoc(text), def = new Map();
  for (const b of doc.bags) if (!def.has(b.name)) def.set(b.name, b.number);
  const cites = new Map(); let bib = null; const usedIn = new Map();
  const cnum = (key) => { if (!cites.has(key)) cites.set(key, cites.size + 1); return cites.get(key); };
  for (const b of doc.bags) for (const k of b.bag.kids) if (k instanceof Code && isFence(k))
    for (const l of fenceLines(k)) { const m = REF.exec(l); if (m) { const n = m[2].trim(); (usedIn.get(n) || usedIn.set(n, []).get(n)).push(b); } }
  let seq = 0; const bagNo = new Map(); for (const b of doc.bags) bagNo.set(b.bag, b);
  const html = [];
  const inlineCode = (c) => /^\$[\s\S]*\$$/.test(c.text) && c.text.length > 2
    ? "\u0004" + esc(c.text.slice(1, -1)) + "\u0005" : `<code>${esc(c.text)}</code>`;
  const flush = (buf) => {
    const s = buf.join(""); buf.length = 0;
    for (let p of s.split(/\n\s*\n/)) {
      p = p.trim(); if (!p) continue;
      const one = /^\u0004[^\u0004\u0005]*\u0005$/.test(p);
      p = p.replace(/\u0004([^\u0004\u0005]*)\u0005/g, (_, t) => one ? `\\[${t}\\]` : `\\(${t}\\)`);
      html.push(one ? `<div class="display">${p}</div>` : `<p>${p.replace(/\s*\n\s*/g, " ")}</p>`);
    }
  };
  const codeHtml = (k) => fenceLines(k).map((l) => {
    const m = REF.exec(l);
    if (m) { const n = m[2].trim(), d = def.get(n);
      return d ? `${esc(m[1])}<a class="ref" href="#c-${d}">«${esc(n)}»<span class="rn">§${d}</span></a>` : `${esc(m[1])}<span class="ref bad">«${esc(n)}»</span>`; }
    return esc(unescapeLine(l));
  }).join("\n");
  const walk = (bag, buf) => {
    for (const k of bag.kids) {
      if (k instanceof Blob) buf.push(esc(k.text));
      else if (k instanceof Code) { if (isFence(k)) { flush(buf); html.push(`<pre class="code">${codeHtml(k)}</pre>`); } else buf.push(inlineCode(k)); }
      else {
        const t = k.tag.trim();
        if (HEADING.test(t)) { flush(buf); const txt = k.kids.map((x) => x.text ?? "").join(""); html.push(`<${t} id="${slug(txt)}">${esc(txt)}</${t}>`); }
        else if (t === "cite") { const keys = k.kids.map((x) => x.text ?? "").join("").split(",").map((s) => s.trim()).filter(Boolean);
          buf.push(`<span class="cite">[${keys.map((q) => `<a href="#bib-${esc(q)}">${cnum(q)}</a>`).join(", ")}]</span>`); }
        else if (t === "bibliography") bib = k;
        else { flush(buf); const b = bagNo.get(k), isFile = FILE.test(t);
          const used = (usedIn.get(t) || []).map((u) => `<a href="#c-${u.number}">§${u.number}</a>`);
          html.push(`<section class="chunk" id="c-${b.number}"><div class="chunk-head"><a class="num" href="#c-${b.number}">§${b.number}</a> ${isFile ? '<span class="badge">file</span> <span class="cname">' + esc(t) + "</span>" : '<span class="cname">«' + esc(t) + "»</span> ≡"}</div>` +
            (used.length ? `<div class="meta">used in ${used.join(", ")}</div>` : ""));
          const inner = []; walk(k, inner); flush(inner); html.push("</section>"); }
      }
    }
  };
  const top = []; walk(doc.root, top); flush(top);
  if (bib) {
    const rows = bibEntries(bib).map((e) => {
      const f = e.f, venue = f.journal || f.booktitle || f.publisher || "";
      return `<li id="bib-${esc(e.key)}" value="${cnum(e.key)}">${esc(f.author || "")}. <i>${esc(f.title || "")}</i>. ${esc(venue)}${f.year ? ", " + esc(f.year) : ""}.</li>`;
    });
    html.push(`<h2>Bibliography</h2><ol class="bib">${rows.join("")}</ol>`);
  }
  return html.join("\n");
}
function fenceLines(code) {
  let lines = code.text.split("\n");
  if (lines.length && lines[0].trim() === "") lines = lines.slice(1);
  let indent = "";
  if (lines.length && lines[lines.length - 1].trim() === "") { indent = lines[lines.length - 1]; lines = lines.slice(0, -1); }
  return lines.map((l) => (l.startsWith(indent) ? l.slice(indent.length) : l.trimStart()));
}
