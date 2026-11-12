/* ---------- page ---------- */
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const PRESETS = $$("script[data-preset]").map((s) => ({ id: s.dataset.id, name: s.dataset.preset, file: s.dataset.file, text: s.textContent.replace(/^\n/, "") }));
const preset = (id) => PRESETS.find((p) => p.id === id);
const store = { get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }, set(k, v) { try { localStorage.setItem(k, v); } catch (e) {} } };

/* theme lamp */
(function () {
  const root = document.documentElement, saved = store.get("lit-theme");
  if (saved === "dark" || saved === "light") root.dataset.theme = saved;
  $("#lamp").addEventListener("click", () => {
    const dark = root.dataset.theme ? root.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
    root.dataset.theme = dark ? "light" : "dark"; store.set("lit-theme", root.dataset.theme);
  });
})();

/* copy buttons */
document.addEventListener("click", (e) => {
  const b = e.target.closest("[data-copy]"); if (!b) return;
  const text = b.dataset.copy, done = () => { const o = b.textContent; b.textContent = "Copied"; setTimeout(() => (b.textContent = o), 1200); };
  const fallback = () => { const t = document.createElement("textarea"); t.value = text; document.body.appendChild(t); t.select(); try { document.execCommand("copy"); done(); } catch (x) {} t.remove(); };
  try { navigator.clipboard.writeText(text).then(done, fallback); } catch (x) { fallback(); }
});

/* ---------- playground ---------- */
const src = $("#src"), gut = $("#gut"), hl = $("#hl"), trace = $("#trace");
let T = null, srcFile = "merge_sort.lit", hlRows = [], byLitLine = new Map(), lineStarts = [0];
const TRACE_IDLE = "Hover a generated line to see where it came from. Click into the document to see where a line goes.";
trace.textContent = TRACE_IDLE;

function lineOfOffset(off) { let lo = 0, hi = lineStarts.length - 1; while (lo < hi) { const mid = (lo + hi + 1) >> 1; if (lineStarts[mid] <= off) lo = mid; else hi = mid - 1; } return lo + 1; }

function errLine(err, text) {
  if (err.line) return err.line;
  const m = /<<<(.+?)>>>/.exec(err.message); if (!m) return 0;
  const i = text.indexOf("<<<" + m[1] + ">>>"); return i < 0 ? 0 : lineAt(text, i);
}

function drawEditor(text, t, bad) {
  const lines = text.split("\n"); lineStarts = [0];
  for (let i = 0; i < text.length; i++) if (text.charCodeAt(i) === 10) lineStarts.push(i + 1);
  const cls = new Array(lines.length).fill("");
  if (t) for (const b of t.doc.blocks) for (const l of b.lines) { const n = lineOfOffset(l.start) - 1; cls[n] = REF.test(l.text) ? "ref" : "code"; }
  if (bad) cls[bad - 1] = "bad";
  hl.innerHTML = lines.map((l, i) => `<div class="l ${cls[i]}">${esc(l) || " "}</div>`).join("");
  hlRows = [...hl.children];
  gut.innerHTML = lines.map((_, i) => `<div class="${bad === i + 1 ? "bad" : ""}">${i + 1}</div>`).join("");
  syncScroll();
}
function syncScroll() {
  hl.style.transform = `translate(${-src.scrollLeft}px, ${-src.scrollTop}px)`;
  gut.style.transform = `translateY(${-src.scrollTop}px)`;
}
src.addEventListener("scroll", syncScroll);

function setHot(lines) { hlRows.forEach((r, i) => r.classList.toggle("hot", lines.has(i + 1))); }
function setOn(keys) { $$(".gl", $("#out-tangled")).forEach((g) => g.classList.toggle("on", keys.has(g.dataset.f + ":" + g.dataset.i))); }

function renderTangled(t) {
  byLitLine = new Map();
  const names = Object.keys(t.origins);
  if (!names.length) return `<div class="empty">No file yet. A chunk whose name ends in an extension, like <code>#hello.py{ ... }</code>, is written out as a file.</div>`;
  return names.map((name) => {
    const ex = t.origins[name];
    const rows = ex.map((l, i) => {
      const L = lineOfOffset(t.doc.blocks[l.bid].lines[l.li].start);
      (byLitLine.get(L) || byLitLine.set(L, []).get(L)).push(name + ":" + i);
      return `<span class="gl" data-f="${esc(name)}" data-i="${i}" data-l="${L}" data-b="${l.bid}"><i>${i + 1}</i>${esc(l.text) || " "}</span>`;
    }).join("");
    return `<div class="file-head"><span>${esc(name)}</span><span>${ex.length} lines</span></div><pre class="gen">${rows}</pre>`;
  }).join("");
}

function renderChunks(t) {
  const { chunks } = t.doc, kids = new Map(), used = new Set(), missing = new Set();
  for (const [n, lines] of chunks) { const k = [];
    for (const l of lines) { const m = REF.exec(l.text); if (m) { const c = m[2].trim(); k.push(c); used.add(c); if (!chunks.has(c)) missing.add(c); } }
    kids.set(n, k); }
  const defs = new Map(); for (const b of t.doc.bags) defs.set(b.name, (defs.get(b.name) || 0) + 1);
  const node = (n, depth) => {
    const own = chunks.get(n) ? chunks.get(n).length : 0, isFile = FILE.test(n);
    const sub = depth < 8 && kids.get(n) ? kids.get(n).map((c) => node(c, depth + 1)).join("") : "";
    return `<li><span class="${isFile ? "fn" : missing.has(n) ? "cn bad" : "cn"}">${isFile ? esc(n) : "«" + esc(n) + "»"}</span><span class="dim">${missing.has(n) ? "not defined" : own + " lines" + (defs.get(n) > 1 ? ` · defined ${defs.get(n)} times` : "")}</span>${sub ? `<ul>${sub}</ul>` : ""}</li>`;
  };
  const files = [...chunks.keys()].filter((n) => FILE.test(n));
  const unused = [...chunks.keys()].filter((n) => !FILE.test(n) && !used.has(n) && chunks.get(n).length);
  return (files.length ? `<ul class="tree">${files.map((f) => node(f, 0)).join("")}</ul>` : `<div class="empty">No file chunks, so nothing to draw.</div>`) +
    unused.map((n) => `<div class="warn">«${esc(n)}» has code but nothing uses it, so it is never written.</div>`).join("");
}

let wovenDirty = true;
function renderWoven(text) {
  const el = $("#out-woven");
  try { el.innerHTML = weave(text); } catch (e) { el.innerHTML = ""; }
  if (window.MathJax && MathJax.typesetPromise) { try { MathJax.typesetClear([el]); } catch (e) {} MathJax.typesetPromise([el]).catch(() => {}); }
  wovenDirty = false;
}

function showError(err, text) {
  const n = errLine(err, text);
  const html = `<div class="err"><b>${n ? "line " + n : "error"}</b> ${esc(err.message)}${n ? `<br><span class="small-note">${esc(text.split("\n")[n - 1] || "")}</span>` : ""}</div>`;
  $("#out-tangled").innerHTML = html; $("#out-chunks").innerHTML = html; $("#out-woven").innerHTML = html;
  $("#srcStatus").textContent = "does not parse"; $("#srcStatus").style.color = "var(--bad)";
  drawEditor(text, null, n);
}

function update() {
  const text = src.value; let t;
  try { t = tangle(text); } catch (e) { T = null; showError(e, text); return; }
  T = t; drawEditor(text, t, 0);
  const nf = Object.keys(t.files).length;
  $("#srcStatus").textContent = `${t.doc.chunks.size} chunks · ${nf} file${nf === 1 ? "" : "s"}`; $("#srcStatus").style.color = "";
  $("#out-tangled").innerHTML = renderTangled(t);
  $("#out-chunks").innerHTML = renderChunks(t);
  wovenDirty = true; if (!$("#out-woven").hidden) renderWoven(text);
  trace.innerHTML = TRACE_IDLE;
}
src.addEventListener("input", update);

$("#out-tangled").addEventListener("mouseover", (e) => {
  const g = e.target.closest(".gl"); if (!g || !T) return;
  setHot(new Set([+g.dataset.l])); const b = T.doc.blocks[+g.dataset.b];
  trace.innerHTML = `<b>${esc(g.dataset.f)}:${+g.dataset.i + 1}</b> <span class="arrow">→</span> <b>${esc(srcFile)}:${g.dataset.l}</b>  «${esc(b.chunk)}»`;
});
$("#out-tangled").addEventListener("mouseleave", () => { setHot(new Set()); trace.textContent = TRACE_IDLE; });
function caretTrace() {
  if (!T) return; const L = lineAt(src.value, src.selectionStart); const hits = byLitLine.get(L);
  setOn(new Set(hits || []));
  if (hits) { const [f, i] = hits[0].split(":"); trace.innerHTML = `<b>${esc(srcFile)}:${L}</b> <span class="arrow">→</span> ${hits.map((h) => { const k = h.lastIndexOf(":"); return `<b>${esc(h.slice(0, k))}:${+h.slice(k + 1) + 1}</b>`; }).slice(0, 4).join(", ")}${hits.length > 4 ? ", …" : ""}`; }
  else trace.innerHTML = `<b>${esc(srcFile)}:${L}</b> <span class="arrow">→</span> no generated line (prose, or a chunk nothing uses)`;
}
["keyup", "click", "focus"].forEach((ev) => src.addEventListener(ev, caretTrace));
src.addEventListener("blur", () => setOn(new Set()));

$$("#tabs .tab").forEach((b) => b.addEventListener("click", () => {
  $$("#tabs .tab").forEach((x) => x.setAttribute("aria-selected", x === b));
  for (const id of ["tangled", "chunks", "woven"]) $("#out-" + id).hidden = id !== b.dataset.tab;
  if (b.dataset.tab === "woven" && wovenDirty && T) renderWoven(src.value);
}));

function loadPreset(id, scroll) {
  const p = preset(id); if (!p) return;
  src.value = p.text; srcFile = p.file; $("#srcName").textContent = p.file; src.scrollTop = 0; src.scrollLeft = 0;
  $$("#presets .chip").forEach((c) => c.setAttribute("aria-pressed", c.dataset.id === id));
  update();
  if (scroll) $("#try").scrollIntoView({ behavior: "smooth", block: "start" });
}
$("#presets").innerHTML = PRESETS.filter((p) => p.id !== "lab").map((p) => `<button class="chip" data-id="${p.id}" aria-pressed="false">${esc(p.name)}</button>`).join("");
$("#presets").addEventListener("click", (e) => { const c = e.target.closest(".chip"); if (c) loadPreset(c.dataset.id); });
document.addEventListener("click", (e) => { const b = e.target.closest("[data-load]"); if (b) loadPreset(b.dataset.load.replace(".lit", ""), true); });
loadPreset("merge");

/* MathJax, loaded after first paint */
window.MathJax = { tex: { inlineMath: [["\\(", "\\)"]], displayMath: [["\\[", "\\]"]] }, svg: { fontCache: "global" }, startup: { typeset: false } };
{ const s = document.createElement("script"); s.async = true; s.src = "https://cdnjs.cloudflare.com/ajax/libs/mathjax/3.2.2/es5/tex-svg.min.js"; document.head.appendChild(s); }

/* ---------- notation rules ---------- */
const RULES = [
  ["A bag is a chunk", "The toolkit reads any bag as a chunk of the program, except the headings h1 to h6, cite and bibliography. Chunks nest, and names may contain spaces.",
    "#greeting{\n  Say hello.\n  ```\n  print(\"hello\")\n  ```\n}", "twice"],
  ["A long run of ticks is program text", "A code node whose tag is three or more ticks is program text, and belongs to the innermost chunk. Use more ticks than the longest run inside it. One or two ticks stay inline code in the prose. The indentation of the closing run is stripped.",
    "#loop{\n  Count to `n`.\n  ```\n  for i in range(n):\n      print(i)\n  ```\n}", "merge"],
  ["A reference pastes a chunk", "A line of program text holding only a reference is replaced by the chunk, indented like the reference. Order in the document is free; order in the file is yours to choose.",
    "def hello(name):\n    <<<greeting>>>", "files"],
  ["The same name joins up", "Define a chunk twice and the code is concatenated in document order. Introduce each import beside the code that needs it.",
    "#imports{\n  ```\n  import json\n  ```\n}\n\n(later in the document)\n\n#imports{\n  ```\n  import time\n  ```\n}", "twice"],
  ["An extension makes a file", "A chunk named like a file is written out when you tangle. The name may carry a folder, such as rig/model.py.",
    "#app.py{\n  ```\n  <<<imports>>>\n  <<<main>>>\n  ```\n}", "files"],
  ["Math and citations", "A code span starting and ending with a dollar sign is LaTeX. A citation is a bag with a key, and the bibliography holds BibTeX that weave formats and links.",
    "`$e^{i\\pi} + 1 = 0$`\nas shown in #cite{euler1748}.", "math"],
  ["A backslash escapes a reference", "Writing about the notation needs a reference line that is not one. Put a backslash in front; tangle removes exactly one.",
    "\\<<<shout>>>   (written out as: <<<shout>>>)", "escape"],
];
$("#rules").innerHTML = RULES.map((r, i) => `<div class="rule"><h3>${esc(r[0])}</h3><p>${esc(r[1])}</p><pre>${esc(r[2])}</pre><button class="btn" data-load="${r[3]}">Try it in the editor</button></div>`).join("");

/* ---------- toolbelt ---------- */
const TOOLS = [
  ["lit tangle", "Writes every file chunk, and a sourcemap next to them. Before overwriting a file you edited, it keeps a copy.", "lit tangle program.lit out/"],
  ["lit weave", "Interlinked HTML with MathJax, a chunk index and a bibliography. LaTeX too, ready for your own engine.", "lit weave program.lit -o site/"],
  ["lit absorb", "Folds an edit of a generated file back into the document, using git diff and the sourcemap. It refuses rather than guess.", "lit absorb program.lit out/app.py -o out/"],
  ["lit addr", "Translates an address in either direction, for editors and error messages. It also copes with files you edited since tangling.", "lit addr program.lit out/app.py:42:9"],
  ["lit sync", "Three-way merge between the document and its files, and an interactive picker, region by region, in both directions.", "lit sync program.lit pick -o out/"],
  ["lit watch", "Records every change to either side without touching anything, so no edit is ever lost.", "lit watch program.lit -o out/"],
  ["lit lint", "Finds what compilers cannot: stray braces, undefined references, citations with no entry, and unverifiable bibliography items.", "lit lint program.lit --refs"],
  ["lit fill", "Fills each #agent-fill-in{...} gap: gathers context from the whole document, asks an assistant, checks the answer, writes it in.", "lit fill program.lit --agent anthropic"],
  ["lit review", "Checks what was filled in, optionally asks a second assistant, and approves or rejects each fill.", "lit review program.lit --ai"],
];
$("#toolgrid").innerHTML = TOOLS.map((t) => `<div class="tool"><h3>${esc(t[0])}</h3><p>${esc(t[1])}</p><div class="cmd"><code>${esc(t[2])}</code><button class="btn" data-copy="${esc(t[2])}">Copy</button></div></div>`).join("");

/* ---------- the lab ---------- */
const labLit = $("#labLit"), labGen = $("#labGen"), LAB = preset("lab").text, F = "tip.py";
let baseGen = [], baseLit = "", backups = [];
const logEl = $("#log");
const joinLines = (a) => a.join("\n") + (a.length ? "\n" : "");
function say(msg, kind) { const li = document.createElement("li"); if (kind) li.className = kind; li.innerHTML = msg; logEl.prepend(li); }
function reset() {
  labLit.value = LAB; const t = tangle(LAB); baseGen = t.files[F].slice(); baseLit = LAB; labGen.value = joinLines(baseGen); backups = [];
  logEl.innerHTML = ""; say("Tangled <code>tip.lit</code> into <code>tip.py</code>. Both sides start out equal."); refresh();
}
const EDITS = [
  ["Document: tip 18%", () => labLit.value.includes("tip=0.15") && (labLit.value = labLit.value.replace("tip=0.15", "tip=0.18")), "in the document"],
  ["File: split for 3", () => labGen.value.includes("split(84.0, 4)") && (labGen.value = labGen.value.replace("split(84.0, 4)", "split(120.0, 3)")), "in tip.py"],
  ["File: tidy the tip line", () => labGen.value.includes("total = total * (1 + tip)") && (labGen.value = labGen.value.replace("total = total * (1 + tip)", "total *= 1 + tip")), "in tip.py"],
  ["File: tip 20%", () => labGen.value.includes("tip=0.15") && (labGen.value = labGen.value.replace("tip=0.15", "tip=0.20")), "in tip.py"],
];
$("#edits").insertAdjacentHTML("beforeend", EDITS.map((e, i) => `<button class="chip" data-edit="${i}">${esc(e[0])}</button>`).join(""));
$("#edits").addEventListener("click", (e) => {
  const b = e.target.closest("[data-edit]"); if (!b) return; const ed = EDITS[+b.dataset.edit];
  if (ed[1]()) say(`Edited ${ed[2]}: <i>${esc(ed[0].replace(/^(Document|File): /, ""))}</i>.`); else say("That line has already been changed. Start over, or edit the text yourself.", "bad");
  refresh();
});

function diffHtml(base, now) {
  const hs = hunks(base, now); if (!hs.length) return "";
  return hs.map((h) => base.slice(h.s, h.e).map((l) => `<span class="d">- ${esc(l)}</span>`).concat(h.add.map((l) => `<span class="a">+ ${esc(l)}</span>`)).join("\n")).join("\n");
}
function regionHtml(r, i) {
  const at = `line ${r.s + 1}${r.e - r.s > 1 ? "–" + r.e : ""}`;
  if (r.kind === "conflict") return `<div class="reg"><span class="kind conflict">conflict</span><code>${F} ${at}</code>` +
    `<pre class="diff"><span class="who">the document says</span>${diffHtml(r.base, r.lit)}<span class="who">the file says</span>${diffHtml(r.base, r.gen)}</pre>` +
    `<div class="resolve"><button class="btn" data-res="${i}:lit">Keep the document's</button><button class="btn" data-res="${i}:gen">Keep the file's</button><button class="btn" data-res="${i}:both">Keep both</button></div></div>`;
  const label = { lit: "from the document", gen: "from the file", same: "both sides, same change" }[r.kind];
  return `<div class="reg"><span class="kind ${r.kind}">${label}</span><code>${F} ${at}</code><pre class="diff">${diffHtml(r.base, r.kind === "gen" ? r.gen : r.lit)}</pre></div>`;
}
let regions = [];
function refresh() {
  const lit = labLit.value, gen = toLines(labGen.value);
  const dl = lit !== baseLit, dg = JSON.stringify(gen) !== JSON.stringify(baseGen);
  const ls = $("#litState"), gs = $("#genState");
  ls.textContent = dl ? "edited" : "in sync"; ls.classList.toggle("dirty", dl);
  gs.textContent = dg ? "edited" : "in sync"; gs.classList.toggle("dirty", dg);
  let ours;
  try { ours = tangle(lit).files[F]; if (!ours) throw new LitError("the document no longer defines tip.py", 0); }
  catch (e) { regions = []; $("#pending").innerHTML = `<div class="err" style="margin:0"><b>${e.line ? "line " + e.line : "error"}</b> ${esc(e.message)}</div>`; setButtons(false); return; }
  regions = diff3(baseGen, ours, gen);
  const k = (x) => regions.filter((r) => r.kind === x).length;
  $("#pending").innerHTML = regions.length ? regions.map(regionHtml).join("") : `<p class="ok-line" style="margin:0">Both sides agree. Nothing is waiting.</p>`;
  setButtons(true);
  $("#bMerge").disabled = !regions.length; $("#bAbsorb").disabled = !dg && !dl && !regions.length;
  const bk = $("#backups");
  bk.innerHTML = backups.length ? "Saved copies of edits that were replaced: " + backups.map((b, i) => `<button class="btn" data-restore="${i}">Restore #${i + 1}</button>`).join(" ") : "";
}
function setButtons(ok) { ["#bMerge", "#bTangle", "#bAbsorb"].forEach((s) => ($(s).disabled = !ok)); }
labLit.addEventListener("input", refresh); labGen.addEventListener("input", refresh);

$("#bTangle").addEventListener("click", () => {
  let t; try { t = tangle(labLit.value); } catch (e) { say(esc(e.message), "bad"); return; }
  const ours = t.files[F], cur = toLines(labGen.value);
  if (JSON.stringify(cur) === JSON.stringify(ours)) { say("Tangle: tip.py already matches the document."); baseGen = ours.slice(); baseLit = labLit.value; refresh(); return; }
  if (JSON.stringify(cur) !== JSON.stringify(baseGen)) { backups.push(labGen.value); say("Tangle replaced <code>tip.py</code>. It had edits that were not in the document, so a copy was saved. That is the order problem: tangle first, and the file's edit is gone from view.", "bad"); }
  else say("Tangle rewrote <code>tip.py</code> from the document.", "good");
  labGen.value = joinLines(ours); baseGen = ours.slice(); baseLit = labLit.value; refresh();
});
$("#bAbsorb").addEventListener("click", () => {
  if (labLit.value !== baseLit) { say("Absorb refused: the document changed since it was tangled, so the sourcemap is out of date. Use Merge, which handles both.", "bad"); return; }
  const pristine = tangle(labLit.value).files[F], cur = toLines(labGen.value);
  if (JSON.stringify(pristine) === JSON.stringify(cur)) { say("Absorb: nothing to absorb, the file matches the document."); return; }
  const a = absorb(labLit.value, F, pristine, cur);
  if (a.errors.length) { say("Absorb refused: " + esc(a.errors[0]), "bad"); return; }
  labLit.value = a.text; baseLit = a.text; baseGen = cur.slice();
  say("Absorbed the file's edits into the document. Order problem, other direction: any edit you had made in the document and not tangled would have blocked this.", "good"); refresh();
});
function doMerge(decisions) {
  const lit = labLit.value, gen = toLines(labGen.value);
  let r; try { r = mergeFile(lit, F, baseGen, gen, decisions); } catch (e) { say(esc(e.message), "bad"); return; }
  if (r.errors.length) { say("Merge refused: " + esc(r.errors[0]), "bad"); return; }
  const n = (x) => r.regions.filter((q, i) => q.kind === x && !decisions[i]).length, left = r.regions.filter((q, i) => q.kind === "conflict" && !decisions[i]).length;
  const parts = [];
  if (n("lit")) parts.push(`${n("lit")} change${n("lit") > 1 ? "s" : ""} from the document went into the file`);
  if (n("gen")) parts.push(`${n("gen")} edit${n("gen") > 1 ? "s" : ""} from the file went into the document`);
  if (Object.keys(decisions).length) parts.push("your choice was applied");
  labLit.value = r.newLit; labGen.value = joinLines(r.gen); baseGen = r.base; baseLit = r.newLit;
  say("Merged: " + (parts.join("; ") || "nothing to do") + "." + (left ? ` ${left} conflict${left > 1 ? "s" : ""} left for you.` : " Both sides agree now."), left ? "bad" : "good");
  refresh();
}
$("#bMerge").addEventListener("click", () => doMerge({}));
$("#pending").addEventListener("click", (e) => { const b = e.target.closest("[data-res]"); if (!b) return; const [i, c] = b.dataset.res.split(":"); doMerge({ [i]: c }); });
$("#backups").addEventListener("click", (e) => { const b = e.target.closest("[data-restore]"); if (!b) return; labGen.value = backups[+b.dataset.restore]; say("Restored a saved copy into <code>tip.py</code>."); refresh(); });
$("#bReset").addEventListener("click", reset);
reset();

/* ---------- the tree inspector ---------- */
const TREES = [
  ["A card", "#card{\n  #front{\n    This is front.\n  }\n  #back{\n    This is back.\n  }\n}\n"],
  ["Hello and code", "Hello, World!\n\n#comment{\n  A first program!\n}\n\nCode to compute the nth Fibonacci number.\n\n`\ndef fibs(n):\n    if n == 0 or n == 1:\n        return 1\n    return fibs(n-1) + fibs(n-2)\n`\n\nThat is all.\n"],
  ["More ticks", "A code node needs more ticks than the longest run inside it.\n\n``\nSome `code` here.\n``\n\n```\nA run of `` inside needs three.\n```\n"],
  ["Break it", "A stray } in running text closes the tree early.\n\n#note{\n  Put it in ticks instead: `}`\n}\n"],
];
const treeSrc = $("#treeSrc");
const snip = (s, n = 70) => { const t = s.replace(/\s+/g, " ").trim(); return t.length > n ? t.slice(0, n) + "…" : t; };
function nodeHtml(n) {
  if (n instanceof Bag) { const kids = n.kids.map(nodeHtml).join("");
    return `<li><span class="nk bag">bag</span> <span class="cn">#${esc(n.tag)}</span>${kids ? `<ul>${kids}</ul>` : ""}</li>`; }
  if (n instanceof Code) return `<li><span class="nk code">code</span> <span class="dim">${n.tag.length} tick${n.tag.length > 1 ? "s" : ""}</span><span class="snip">${esc(n.text.replace(/^\n|\n$/g, ""))}</span></li>`;
  if (n.text.trim() === "") return "";
  return `<li><span class="nk blob">blob</span> <span class="q">${esc(snip(n.text))}</span></li>`;
}
function drawTree() {
  const text = treeSrc.value, out = $("#treeOut"), st = $("#treeStatus");
  try {
    const root = load(text);
    out.innerHTML = `<ul class="tree"><li><span class="nk bag">bag</span> <span class="cn">the whole text</span> <span class="dim">(an implicit bag around it)</span><ul>${root.kids.map(nodeHtml).join("")}</ul></li></ul>`;
    st.textContent = "parses"; st.style.color = "";
  } catch (e) {
    out.innerHTML = `<div class="err"><b>${e.line ? "line " + e.line : "error"}</b> ${esc(e.message)}${e.line ? `<br><span class="small-note">${esc(text.split("\n")[e.line - 1] || "")}</span>` : ""}</div>`;
    st.textContent = "does not parse"; st.style.color = "var(--bad)";
  }
}
$("#treeChips").innerHTML = TREES.map((t, i) => `<button class="chip" data-tree="${i}" aria-pressed="false">${esc(t[0])}</button>`).join("");
$("#treeChips").addEventListener("click", (e) => {
  const c = e.target.closest("[data-tree]"); if (!c) return;
  $$("#treeChips .chip").forEach((x) => x.setAttribute("aria-pressed", x === c));
  treeSrc.value = TREES[+c.dataset.tree][1]; drawTree();
});
treeSrc.addEventListener("input", () => { $$("#treeChips .chip").forEach((x) => x.setAttribute("aria-pressed", "false")); drawTree(); });
$("#treeChips .chip").click();

/* ---------- Tab inserts a tab; Esc then Tab leaves the editor ---------- */
function typeInto(ta, text) {
  ta.focus();
  if (!document.execCommand("insertText", false, text)) { ta.setRangeText(text, ta.selectionStart, ta.selectionEnd, "end"); ta.dispatchEvent(new Event("input", { bubbles: true })); }
}
function tabbable(ta) {
  let escaped = false;
  ta.addEventListener("blur", () => (escaped = false));
  ta.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { escaped = true; return; }
    if (e.key !== "Tab" || e.ctrlKey || e.metaKey || e.altKey) { if (e.key !== "Shift") escaped = false; return; }
    if (escaped) { escaped = false; return; }
    e.preventDefault();
    const v = ta.value, s = ta.selectionStart, en = ta.selectionEnd;
    if (s === en && !e.shiftKey) { typeInto(ta, "\t"); return; }
    const ls = v.lastIndexOf("\n", s - 1) + 1;
    let le = v.indexOf("\n", en > s && v[en - 1] === "\n" ? en - 1 : en); if (le < 0) le = v.length;
    const lines = v.slice(ls, le).split("\n");
    const out = lines.map((l) => (e.shiftKey ? l.replace(/^(\t| {1,4})/, "") : "\t" + l));
    const block = out.join("\n"), delta = block.length - (le - ls);
    ta.setSelectionRange(ls, le); typeInto(ta, block);
    if (s === en) { const first = out[0].length - lines[0].length; const p = Math.max(ls, s + first); ta.setSelectionRange(p, p); }
    else ta.setSelectionRange(ls, le + delta);
  });
  const head = ta.closest(".pane") && ta.closest(".pane").querySelector(".pane-head");
  if (head) { const h = document.createElement("span"); h.className = "hint"; h.textContent = "Tab inserts a tab · Esc, then Tab, leaves"; head.appendChild(h); }
}
["#src", "#labLit", "#labGen", "#treeSrc"].forEach((s) => tabbable($(s)));
