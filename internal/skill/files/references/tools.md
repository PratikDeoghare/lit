# The toolchain

Contents: overview · tangle · lint · weave · sourcemap and addr · absorb · sync and watch · fill and review · troubleshooting

Everything is a set of Python scripts in this skill's `scripts/` folder, run with `python3` (3.9 or newer) and nothing else: only
the standard library, and a copy of the brashtag parser (`_brashtag.py`) sits beside them. Optional extras: `git` is not needed (the diff
is built in), a LaTeX installation (`xelatex`) is only needed to compile the `.tex` you get from weave, `rsvg-convert` or `inkscape` only to put
SVG figures into that LaTeX, and `gofmt` only to syntax-check generated Go in `fill.py`. Every script prints its usage with `-h`.

People who prefer one binary can use `lit`, a Go program with the same commands and the same files (`lit tangle`, `lit fill`, ...);
it is optional and this skill never needs it. Both read and write the same `.lit` and `.fills.jsonl`; their sourcemaps are not
interchangeable (the Go one counts bytes, the Python one characters), so use one tool family per tangle directory.

| command | does | typical use |
|---|---|---|
| `tangle.py` | `.lit` to source files, plus a sourcemap | `python3 scripts/tangle.py prog.lit out` |
| `lint.py` | structure, citations, bibliography checks | `python3 scripts/lint.py prog.lit --refs` |
| `weave.py` | `.lit` to cross-linked HTML and LaTeX | `python3 scripts/weave.py prog.lit -o woven` |
| `addr.py` | translate addresses between `.lit` and generated files | `python3 scripts/addr.py prog.lit out/x.py:12` |
| `absorb.py` | fold edits of generated files back into the `.lit` | `python3 scripts/absorb.py prog.lit -o out` |
| `sync.py` | three-way merge of .lit and generated files; interactive cherry-pick | `python3 scripts/sync.py prog.lit merge -o out` |
| `watch.py` | records edits on both sides (never modifies files) | `python3 scripts/watch.py prog.lit -o out` |
| `fill.py` | an agent writes the code at `#agent-fill-in{}` hints | `python3 scripts/fill.py prog.lit --agent anthropic` |
| `review.py` | checks, second-opinions, approves or rejects what was filled | `python3 scripts/review.py prog.lit --check 'python3 -m unittest'` |

## tangle

`python3 scripts/tangle.py SOURCE.lit [OUTDIR] [--no-map]`. Writes every file chunk under OUTDIR (default `.`), and
`OUTDIR/SOURCE.lit.map`. Prints warnings for unused chunks. Malformed input stops with a line number; see the
error table in `notation.md`. If SOURCE.lit contains `#include{chapter.lit}` the chapter files are spliced in first (see
Chapters in `notation.md`); every tool takes the root, and names chapter files and their own line numbers. The map is built from tangle's own expansion and checked against it, so it cannot
disagree with the files.

## lint

`python3 scripts/lint.py SOURCE.lit [--refs]`. Exit status 1 on errors (parse failure, undefined chunk, dangling citation,
bibliography entry without author, title or year), 0 otherwise. Warnings: unused or empty chunks, more than 40 lines of
code with no prose, uncited entries, entries with no arXiv id, DOI, ISBN or URL. `--refs` lists every reference
with its identifier as the verification checklist. The final line gives chunk, code-line, prose-word and
citation counts; a document with almost no prose relative to code usually needs more explanation.

## weave

`python3 scripts/weave.py SOURCE.lit [-o OUTDIR] [--only html|tex] [--mathjax URL]`

- **HTML** (`SOURCE.html`): single file, sidebar table of contents, chunks numbered `§n`, every `<<<reference>>>` linked to its
  chunk, "used in" and "part of file" lines, numbered citations linked to the bibliography (which links back), a chunk
  index. Math is typeset by MathJax 3 loaded from a CDN, so viewing needs a network connection; pass `--mathjax` with a
  URL or local path for offline use. Code is written as plain text so the files stay readable as source; there is no syntax colouring.
- **Figures** (`#img{`pic.svg` Caption.}`): see Figures in `notation.md`. SVG goes into the HTML as it is; LaTeX gets a PDF
  made by `rsvg-convert` or `inkscape` when one is installed, and otherwise a note.
- **LaTeX** (`SOURCE.tex`): the same structure with `hyperref` links, `\cite` and `thebibliography`, page numbers in cross
  references, a chunk index. Compile with `xelatex SOURCE.tex` (or `lualatex` or `tectonic`: they read Unicode, which pdflatex does not; run twice for
  references). Needs `amsmath amssymb xcolor fancyvrb hyperref longtable iftex`; `fvextra` and `lmodern` are used if
  present. The tool does not produce a PDF itself.

The weaver warns about undefined references and citations without bibliography entries. Math that MathJax
cannot read shows as red source text; fix the LaTeX.

## sourcemap and addr

`tangle.py` writes `OUTDIR/SOURCE.lit.map`: JSON listing every fenced block with the character offsets of its lines
in the `.lit`, and for each generated file runs of lines `[at, n, block, line, prefix]` (`prefix` is the indentation
added by references). `addr.py` reads it:

```bash
python3 scripts/addr.py prog.lit rig/model.py:64:13 -o out     # generated -> .lit : prog.lit:158:9   «chunk»
python3 scripts/addr.py prog.lit prog.lit:158:9 -o out         # .lit -> every generated place
python3 scripts/addr.py prog.lit 158 -o out                    # a bare line is a .lit line
python3 scripts/addr.py prog.lit out/rig/model.py:64 --json    # machine-readable
```

Direction is detected from the file part. A chunk expanded in several places returns several answers. A
`<<<reference>>>` line returns where that chunk is expanded; prose returns nothing (exit 1). If a generated file has
been edited since tangling, lines are matched against the pristine output so the numbers refer to the current file.
The tool refuses to answer when the `.lit` changed after tangling, because the offsets would be wrong (`--ignore-stale`
overrides, at your own risk).

## absorb

`python3 scripts/absorb.py SOURCE.lit [FILE ...] [-o OUTDIR] [--dry-run] [--force]`. Use it when code was edited directly in
generated files and the change belongs in the `.lit`.

1. It checks that the `.lit` is unchanged since tangling, re-tangles in memory for the pristine files, and diffs
   (a built-in line diff) each edited file against its pristine version.
2. Each changed or deleted line is mapped to the `.lit` line that produced it; the indentation added by references
   is removed and the block's own indentation restored. Inserted lines go after the preceding line's origin (before the
   first line when inserted at the top); a note appears when that choice sits on a boundary between chunks.
3. The patched `.lit` is tangled in memory and compared with the files on disk. If they match exactly it is written, and the
   files and map refreshed. Otherwise it stops and shows the differences.

Always run with `--dry-run` first and read the printed diff of the `.lit`. A note such as *lines inserted after line 19
sit at a chunk boundary; placed at the end of «A» (they could also start «B»)* means absorb chose by position. The
result tangles correctly either way, but it is your job to put the code where a reader expects it: usually move the lines
into the right chunk, or, for new logic, create a named chunk with prose and reference it where the lines were.

Refusals you will meet, and what to do:

- *Chunk used in several places, one copy edited:* absorbing would change the other copy too. Make the same edit in every
  copy (then it is accepted), or pass `--force` to let the `.lit` win and regenerate the others.
- *Copies edited differently:* conflict; make them agree.
- *Indented less than the references add:* the extra indentation comes from a `<<<reference>>>` in the `.lit`; change that
  line there.
- *The `.lit` changed since it was tangled:* tangle again (this overwrites edits to generated files) or absorb first.
- *Patched `.lit` no longer parses:* an edited line contains the fence delimiter or a similar construct.

Absorb handles code only. It never edits prose, headings or chunk structure, and it cannot create new chunks or files:
write those in the `.lit`. With named files, other edited files are left alone and stay absorbable later.

## sync and watch: when both sides changed

Editing the `.lit` and a generated file separately makes `tangle.py` and `absorb.py` order-dependent: whichever runs
first discards the other edit. `tangle.py` and `absorb.py` now record a **base** (the generated files at the last sync) in
`OUTDIR/SOURCE.lit.sync/`. `sync.py` merges three ways in generated-file space: base, `tangle(.lit)`, and the files on disk.

```bash
python3 scripts/sync.py prog.lit status -o out            # what changed where (--check exits 1 if anything is pending)
python3 scripts/sync.py prog.lit diff -o out              # every region: from the .lit / edited in a file / conflict
python3 scripts/sync.py prog.lit merge -o out             # cross over all non-conflicting changes, both ways
python3 scripts/sync.py prog.lit merge -o out --prefer lit|gen|newer    # also decide conflicts
python3 scripts/sync.py prog.lit pick -o out              # interactive, like git add -p, both directions
python3 scripts/sync.py prog.lit log -o out ; scripts/sync.py prog.lit restore FILE --list
python3 scripts/sync.py prog.lit adopt --base-lit OLD.lit # project tangled before sync existed (or --gen-is-base)
python3 scripts/watch.py prog.lit -o out                  # optional: journal edits as they happen
```

Unchosen changes stay pending (the base keeps the old text), conflicts are never decided silently, a chunk used in several
places refuses unless every copy was edited alike (or `--force`), and everything replaced is kept as a content-addressed
snapshot. `tangle.py` also saves a copy of an unsynced generated-file edit before overwriting it. Run `merge --dry-run` or
`pick` before trusting a large merge. Order of editing no longer matters; the result is the same.

## fill and review: agent hints

A bag `#agent-fill-in{ what the code here must do }` inside a chunk marks code that is still missing. It is part of the document
(the weaver shows it as a note, `lint.py` warns that it is open) and tangle leaves it out of the files, saying how many hints
are open. Bags whose tag is `agent-fill-in` are reserved: they never define a chunk.

`python3 scripts/fill.py SOURCE.lit [--list] [--dry-run] [--show-context] [--chunk NAME] [--only N] [--limit N] [--agent SPEC] [--model M]
[--check 'CMD'] [--check-each] [--retries N] [--budget CHARS] [--drop-hint]`

- `--list` numbers the open hints in document order (`file:line  «chunk»  hint`). `--dry-run` prints the system text and the prompt
  for hint `--only N` (default 0) and stops; `--show-context` prints just the sections and the size.
- **The context** for a hint, in the order the prompt gives it: the task (hint, chunk, the files it ends up in, the language); the
  heading path; the program's introduction; the prose of the chunks that enclose this one and of the section; where the chunk is
  used, walking from each use site up to the files (the code around each `<<<reference>>>`, up to four levels); the chunks this one
  expands or the hint names, with their prose and code; the chunk in document order with the gap marked; and the tangled file with the
  gap marked, which is what shows the real indentation and neighbours. `--budget` (default 24000 characters) trims the least
  important sections first.
- **The agent** is `--agent anthropic` or `anthropic:MODEL` (the Messages API; needs `ANTHROPIC_API_KEY`, optional `ANTHROPIC_BASE_URL`
  and `LIT_MODEL`), or any shell command that reads the prompt on standard input and prints the reply (`--agent 'claude -p'`). The
  default comes from `LIT_AGENT`. It must answer with one fenced block of code, or one line `CANNOT: reason`.
- **Checks before anything is written:** the reply must hold code; the whole program must still parse and tangle; every generated `.py` must
  compile, every `.json` be valid JSON and every `.go` pass `gofmt -e` when gofmt is installed; the lines must not be empty or look like placeholders. With `--check 'CMD'` the program is
  also tangled into a scratch directory and CMD run there, when the fill closes the last open hint (`--check-each`: after every fill,
  which only makes sense when open hints do not leave the program uncompilable). A failed check is sent back to the agent with its
  output, `--retries` times (default 1); if it still fails the hint is left as it was and `fill.py` exits 1.
- **The result** replaces the hint by the hint's words as prose (`--drop-hint` omits them) and a fenced block, and one line is added
  to `SOURCE.lit.fills.jsonl` (id, chunk, place, hint, the text before and after, agent, attempts, status). Hints are filled one at a
  time and the program is read again each time, so a fill is context for the next. Chapters work: the text goes to the chapter that
  holds the hint.

`python3 scripts/review.py SOURCE.lit [--all] [--check 'CMD'] [--ai [--agent SPEC]] [--approve ID ...] [--approve-all] [--reject ID ...] [--full] [--json]`
reviews every pending fill (`--all`: every fill ever made). For each it says whether the code is still in the `.lit` as filled or was
edited since; repeats the program and code checks (`--check` also runs a command); and with `--ai` asks the agent for a sceptical second
opinion (`VERDICT: ok|concerns|reject` and a few points: does it do what the hint says, bugs, invented identifiers, contradiction with
the prose). Exit 1 if a check failed or the verdict is `reject`. `--approve ID` records that someone read it; `--reject ID` puts the
original hint back in place of the code (refused if the filled text was edited since). A second opinion never replaces reading the code.

## Troubleshooting

| symptom | cause and fix |
|---|---|
| `ModuleNotFoundError: tangle` | run the script by its path, `python3 /path/to/skill/scripts/fill.py ...`; the scripts import their neighbours |
| `fill.py` says `no agent` | pass `--agent 'COMMAND'`, or `--agent anthropic` with `ANTHROPIC_API_KEY` set, or set `LIT_AGENT` |
| tangle fails with a line number | see the error table in `notation.md` |
| HTML shows math as `\( ... \)` text | MathJax did not load: offline, or the CDN is blocked; use `--mathjax` with a local copy |
| LaTeX: `Missing \endcsname` or similar in verbatim | do not hand-edit the generated `.tex`; regenerate it. Chunk labels are deliberately hyphen-free |
| LaTeX: undefined references on the first pass | normal; run `xelatex` twice |
| `addr` / `absorb` say the map is stale | the `.lit` changed after tangling; tangle again |
| CRLF line endings | the map uses character offsets: convert the `.lit` to LF |
