---
name: brashtag-literate
description: Write, convert and maintain literate programs in brashtag notation (.lit files built from #tag{...} bags, named code chunks joined by <<<references>>>, #cite{} citations and a #bibliography{}). Use this skill whenever the user mentions literate programming, brashtag, .lit files, tangle or weave, or wants code, an algorithm, a research-paper implementation or a notebook turned into a narrative document that is also the source, with explanations, math and references to real papers; also when they want a literate source rendered to HTML, LaTeX or PDF, want a tangled file's sourcemap or addresses, or want edits made directly to generated code folded back into the .lit, or want an assistant to fill in code at #agent-fill-in{} hints and review it. Use it even if they only say "make this a literate program" or "explain this code as a document I can tangle back into code", and whenever a message contains #name{ ... } notation with <<<chunk>>> references.
---

# Brashtag literate programs

A literate program is one text that is both the explanation and the source. Code is cut into small
named **chunks**, each presented next to the prose that justifies it, in the order that is best for
a reader. A tool (**tangle**) stitches the chunks back into ordinary source files; another (**weave**)
renders the document as cross-linked HTML and LaTeX.

This skill covers the whole job: choosing a narrative, writing it in brashtag notation, finding
and verifying real references, and running the toolchain, which is a handful of Python scripts in `scripts/`.

## Set up

The toolchain is Python 3.9+ using only the standard library: no packages, no network except what `fill.py` is asked to
call. The scripts are in `scripts/` next to this file, so run them from this skill's folder or by full path:

```bash
python3 scripts/tangle.py prog.lit out        # and weave, lint, absorb, sync, watch, addr, fill, review
```

Each script prints its usage with `-h`. They import each other and the bundled parser `_brashtag.py`, so keep the folder together.
Writing a `.lit` needs no setup at all. (A Go program `lit` offers the same commands as one binary for people who prefer that; it
is optional, uses the same files, and is not needed here.)

## The notation in one screen

Everything is a **bag** `#tag{ ... }` holding text, inline code and other bags. Code spans are
delimited by a run of backticks. That is the whole grammar; this toolchain adds conventions:

````
#h1{Title}                       first h1 is the document title; h2..h6 are sections
Prose, with math `$O(n \log n)$` and a citation #cite{knuth1984lp}.

#name of a chunk{                a bag whose tag is not h1-h6, cite or bibliography is a chunk
  Prose that explains the code below.      (prose and code may alternate freely)

  ```
  def f(x):
      <<<the interesting part>>>       a reference, alone on its line: expands that chunk here
  ```

  #the interesting part{                  chunks nest for organisation; names are global
    ```
    return x + 1
    ```
  }
}

#prog.py{                        a chunk whose name ends in an extension is written to that file
  ```
  <<<name of a chunk>>>
  ```
}

#bibliography{
  #knuth1984lp{ ```@article{knuth1984lp, author = {...}, title = {...}, year = {1984}}``` }
}
````

Rules that prevent most failures (the full list, with error messages, is in `references/notation.md`):

- **A stray `#` or `}` in prose breaks the document**, because `#` opens a bag and `}` closes one.
  Put anything containing them in backticks. The tools report the line.
- **Inside a fence the closing fence's indentation is stripped from every line**, so indent the
  closing fence exactly like the code. Fences of 3+ backticks are code; 1 or 2 backticks are inline code.
- A fence that must contain triple backticks (a README) uses four.
- A code line that must stay literally `<<<name>>>` (docs about the notation) is written `\<<<name>>>`.
- `<<<name>>>` must stand alone on its line. Its indentation is added to every line of the
  expansion, which is how nested Python stays correctly indented; blank lines get none.
- Math is only recognised as an inline code span `` `$...$` ``. A paragraph holding just one such
  span is display math. Write plain LaTeX with amsmath/amssymb.
- Do not end a chunk name in `.something` (for example `step 2.1`): that is how files are recognised.
- Every `#cite{key}` needs a bag with the same key under `#bibliography{}`; list items start with `- `.

`assets/example.lit` is a complete small program using every feature. Read it before writing your first
document, and copy its shape.

## Workflow

Follow these steps in order. Steps 2 and 6 are where most of the quality comes from.

1. **Pin down the job.** Is there existing code to convert (then the tangled result must behave
   identically, and a copy of the original is needed for comparison), or is this new? Who is the
   audience? Which files must come out? Which papers or books lie behind the ideas?
2. **Understand before writing.** Read the code or spec completely. Run it if you can, and keep its
   output as the reference. List the ideas, formulas, algorithms and design choices a reader must
   understand; each is a candidate for prose, and for a citation.
3. **Plan a narrative, not a file layout.** Order the explanation the way you would teach it to a
   sharp colleague: the problem, the key idea, the core data structures, the algorithm, the driver,
   the tests. Source files appear last, in an "Assembly" section that only places chunks. See
   `references/authoring.md` for skeletons by program type.
4. **Cut the code into chunks.** One idea per chunk, usually 3 to 30 lines, named for what it
   achieves ("take the smaller head", "check arguments"), never "part 2". Split a long function
   into named steps and let the parent show its shape. Imports, boilerplate and `main` go in the
   assembly chunks.
5. **Write prose that explains why.** State the intent, the invariant, the complexity, the trade-off
   and the failure case. Define symbols before using them. Do not narrate lines the code already
   makes obvious. Put the explanation directly above the code it justifies. **Test every claim you can
   run**: if the prose says an expression overflows, a variance is `$d_k$`, or an input fails with a given
   error, execute a snippet and look. Unverified claims about behaviour are the commonest way a
   literate document ends up confidently wrong.
6. **Add real references** (`references/citations.md`). Cite where a technique, formula, dataset or
   design choice comes from. Find each paper with a search tool, confirm title, authors, venue,
   year and identifier against the source, and never write a reference from memory without saying
   so. A short, accurate bibliography beats a long doubtful one.
7. **Assemble and verify.**
   ```bash
   python3 scripts/lint.py prog.lit --refs          # structure, citations, bibliography; lists refs to verify
   python3 scripts/tangle.py prog.lit out           # writes the files and a sourcemap
   diff -r original_dir out                        # conversions: identical, or only whitespace (diff -B -w)
   ```
   Run the program or its tests from `out/`. Fix the `.lit`, never the generated files, unless you
   plan to use `absorb.py` (below).
8. **Render and deliver.**
   ```bash
   python3 scripts/weave.py prog.lit -o woven       # prog.html (math via MathJax CDN) and prog.tex
   ```
   Compile the LaTeX yourself only if the user wants a PDF (run `xelatex prog.tex` twice; not pdflatex, which cannot read all of Unicode). Deliver the `.lit` first, then the tangled files, then the
   woven output. Say which references you verified, which you could not, and any behaviour you changed.

## Figures

`#img{`diagram.svg` Caption.}` puts a figure into the woven page. Make the picture a file chunk named `diagram.svg` that holds the SVG text,
so it is part of the program, or point at a file next to the root. Prefer SVG with `currentColor`. See `references/notation.md`.

## Large programs: chapters

Split a big program over files: a root `.lit` that introduces the whole and lists `#include{chapter.lit}`
(paths relative to the including file; chapters may nest). Chunk names stay global across chapters, so one
chapter may reference another's chunks. Every tool takes the root; messages and `addr.py` addresses name
the chapter file and the line in it, `absorb.py` and `sync.py` edit only the chapters that change. Each
chapter must be balanced on its own and may be included once. Details: `references/notation.md`
(Chapters) and `references/authoring.md` (Splitting into chapters).

## Maintaining an existing literate program

- Edit the `.lit`. Chunk names are the anchors that the index and cross-links use; rename them rarely.
- If code was edited directly in a generated file, fold it back instead of redoing it by hand:
  `python3 scripts/absorb.py prog.lit -o out --dry-run`, then without `--dry-run`. It maps each
  changed line to the chunk that produced it, restores indentation, and checks that re-tangling
  reproduces your files. It refuses when a chunk is expanded in several places and only one copy
  was edited (`--force` lets the `.lit` win). Details in `references/tools.md`.
- **Curate after absorbing.** Absorb places lines by position and prints a note when that position is a
  boundary between chunks. Read the printed `.lit` diff, move the lines to the chunk where they belong,
  and if the edit is new logic, give it its own named chunk and a sentence of explanation, because
  absorb never writes prose. Then tangle again and confirm the files still equal the edited ones.
- **Both sides edited** (the `.lit` and a generated file): do not run `tangle.py` or `absorb.py` first, one of them discards
  the other side. Run `python3 scripts/sync.py prog.lit status -o out`, then `merge` (or `pick` to choose region by region).
  See `references/tools.md`.
- `python3 scripts/addr.py prog.lit rig/model.py:57:9` turns a generated address into a `.lit`
  address, and `prog.lit:158` into every generated address. This is how to answer "where does this
  line come from?" and "where does this chunk end up?".
- Re-run `lint.py`, tangle and weave after every substantial change.

## Turning an existing codebase literate (use AI, keep your own fork)

Making an existing program literate is mostly reading and explaining, which is what an assistant is good at, so do it
with the assistant and keep the result as a *view* of the code, not a replacement of it. The one thing everybody agrees
on is the tangled output: the same files the project always had. How the narrative is organised, in what order, with how
much prose and which chapters, is a matter of taste, so everybody can have their own literate fork, written by their own
assistant to their own liking, and nobody has to agree on it. Two readers of the same codebase can hold two different
books that tangle to identical files.

So when asked to convert code: work on a copy in the same repository (for example `docs/lit/`), keep the tangled
result byte for byte equal to the original (`tangle.py` then `diff -r`), never change the project's real layout or build,
and state at the end that the `.lit` is the user's personal view. Do not add the `.lit` to anyone else's workflow.

## Teammates who do not work literate: absorb

Not everybody wants to work literate, and nobody has to. A teammate edits the generated files as always, in their editor,
with their review process. Those edits are not lost and do not have to be redone by hand: `python3 scripts/absorb.py prog.lit --dry-run`
shows how each edit would land in the `.lit`, and `python3 scripts/absorb.py prog.lit` writes it there (code only; the prose is yours to
update). When both sides changed, use `python3 scripts/sync.py prog.lit status` and `merge`. The literate fork follows the project, the
project does not follow the fork.

## Agent hints: leave gaps for an assistant to fill

Put `#agent-fill-in{ what the code here must do }` inside a chunk where code is missing. It is a bag like any other, so it is
part of the prose, and tangle leaves it out of the generated files (with a note that a hint is still open).

```bash
python3 scripts/fill.py prog.lit --list                 # the open hints, in document order
python3 scripts/fill.py prog.lit --dry-run --only 0     # the prompt that would be sent for hint 0, to see what context it gets
python3 scripts/fill.py prog.lit --agent anthropic --check 'python3 -m pytest -q'     # fill them all
python3 scripts/review.py prog.lit --check 'python3 -m pytest -q' --ai                # check what was filled; --approve ID / --reject ID
```

For each hint `fill.py` gathers the context itself: the section's headings and prose, the chunk with the gap marked, the
chunks that use it up to the files, the chunks it uses or the hint names, and the tangled file around the gap. It sends that
to the agent (a command that reads the prompt on stdin, such as `claude -p`, or the Anthropic API with `ANTHROPIC_API_KEY`),
checks the answer (the program must still parse and tangle, generated Python, JSON and (with gofmt installed) Go must be valid, no placeholders; `--check`
runs a command such as the compiler and retries with its error), and writes the code into the `.lit`, keeping the hint's
words as prose. Every fill is recorded in `prog.lit.fills.jsonl`. Never accept a fill unread: run `review.py`, read the
code, and approve it or send the hint back with `--reject`.

When *you* are the assistant asked to write the hint's code, follow the same discipline: read the chunk, the chunks around
it and the tangled file first, match the style and the names in use, and write only the code for the gap.

## Common mistakes

- Prose that restates the code line by line, or a wall of code with no prose. `lint.py` flags the latter.
- Reordering code in a way that changes behaviour, or quietly "improving" code during a conversion.
  Keep behaviour identical and list any deliberate change separately.
- Bibliography entries invented or assembled from partial memory. Verify, or flag.
- Writing a bibliography field you did not see in a source (venue, pages, DOI). Leave it out; an entry with
  only verified fields is correct, and `python3 scripts/lint.py --refs` shows what each entry can be checked against.
- Citing a paper for a claim it does not make. Read enough of the source to know what it supports.
- Pasting long passages from papers. Paraphrase, and quote only a few words when the wording matters.
- Forgetting that a chunk used twice is one piece of text: editing it changes both places.

## Reference files

- `references/notation.md`: full grammar, every convention, error messages and their fixes. Read when
  a tool reports a parse problem or you need a feature not shown above.
- `references/authoring.md`: narrative skeletons, chunking and naming, prose style with before/after
  examples, Python indentation, converting existing code. Read before planning a document.
- `references/citations.md`: finding, verifying and formatting references; BibTeX fields the tools
  read; how to word and place citations. Read before adding any reference.
- `references/tools.md`: every command, its options and outputs, the sourcemap, absorb's safety checks,
  LaTeX and MathJax notes, troubleshooting.
