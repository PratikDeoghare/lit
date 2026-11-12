# Brashtag notation: grammar, conventions, errors

Contents: the grammar · consequences for prose · chunks · code fences · references · files ·
text, math, lists, headings · citations · what the tools do with it · errors and fixes

## The grammar

Brashtag has three kinds of node:

- **Bag** `#tag{ kids }`. The tag is everything between `#` and the first `{`. Kids are blobs,
  code and further bags. Bags nest to any depth.
- **Code** a run of N backticks, then any text, then the same run of N backticks. Everything between
  is verbatim, including `#`, `}` and shorter backtick runs.
- **Blob** plain text, up to the next `#`, backtick or `}`.

The parser reads one bag. The tools therefore wrap the whole file in an implicit `#document{ ... }`, so a
`.lit` is simply a sequence of blobs, code and bags. The parse is lossless (printing the tree gives back the
file), which is how the sourcemap finds character offsets.

## Consequences for prose

Outside backticks, three characters are structural:

| character | effect | what to do |
|---|---|---|
| `#` | starts a bag; the tag runs to the next `{`, possibly across paragraphs | wrap in backticks: `` `C#` ``, `` `#include` `` |
| `}` | closes the enclosing bag, ending the document early if there is none | wrap in backticks, or rephrase |
| `` ` `` | starts a code span | to show a backtick, put it in a span delimited by two backticks |

`{` is harmless in prose. Inside code (inline or fenced) nothing is special except the closing backtick run.
Code, shell commands, JSON, LaTeX with braces: all of it goes inside backticks.

## Tags

- The tag is trimmed of surrounding whitespace for chunk purposes. Spaces inside are fine
  (`#take the smaller head{`). A leading space, a `#` or a newline inside a tag is reported as a probable
  stray `#`.
- Reserved tags: `h1` to `h6` (headings), `cite` (inline citation), `bibliography`, `include`, `img` and `agent-fill-in`. Everything else is a chunk.
- Chunk names are global, case-sensitive, and unique by convention. Defining the same name twice
  **concatenates** the code of both definitions in document order, which lets a chunk be built up in pieces.

## Agent hints: `#agent-fill-in`

`#agent-fill-in{ what the code here must do }` inside a chunk marks a place where code is missing, to be written by an
assistant (`fill.py`, then `review.py`). It sits between prose and fences like any bag; it never defines a chunk, and
a hint outside any chunk is ignored by the tools. Tangle leaves it out of the generated files and says that it is open;
weave shows it as a note; `lint.py` warns. The text of the hint is plain prose (no `#` or `}`; put code in backticks).
When it is filled, the hint's words stay in the chunk as prose and a fenced block follows them. Write hints as a
requirement the code can be checked against ("return the ten most frequent words, ties alphabetical"), not as an
instruction to be polite.

## Chunks

A chunk is a bag that is not reserved. It may hold prose, fenced code, and nested chunk bags, in any
order. Nesting is only for organisation: a nested chunk is referenced by name exactly like a top-level one.
Prose inside a chunk belongs to that chunk in the woven document. Prose and headings at top level are
the narrative around the chunks.

A chunk that is defined but never referenced (and is not a file) contributes nothing to any output;
`lint.py` warns about it.

## Code fences

````
  ```
  def f(x):
      return x
  ```
````

- The newline right after the opening fence is dropped.
- The whitespace in front of the closing fence is the fence's indentation. It is removed from the front of every
  line. Lines that do not start with it lose all leading whitespace instead. So indent the closing fence
  exactly as the code, and keep the code's own relative indentation.
- Blank lines may be empty or hold only spaces; either way they come out empty.
- Use 3 backticks normally and 4 (or more) when the code contains three consecutive backticks.
- A fence with one or two backticks is inline code (prose), not a code block.

## References

A line that holds only `<<<name>>>` (leading whitespace allowed) is replaced by the expansion of chunk `name`.
The line's leading whitespace is prepended to every non-blank line of the expansion, so references nest correctly
inside indented code. Consequences:

- Write the body of a chunk as if it started at column 0; the reference supplies the indentation.
- A chunk referenced from places with different indentation is fine, but it is one piece of text.
- References may nest. A cycle is an error. A reference to an undefined chunk is an error.
- `<<<name>>>` in the middle of a line, or in prose, is not a reference.
- To write a literal `<<<name>>>` line (documentation about this notation, for instance), put a backslash first:
  `\<<<name>>>` alone on a line is written out as `<<<name>>>`. A line that already starts with backslashes before
  `<<<` loses one, so the rule is reversible: add one backslash to every such line when embedding a file verbatim.
- Keep Python's blank-line conventions by putting blank lines between references in the file chunk.

## Files

A chunk whose name ends in `.` plus word characters (`mergesort.py`, `rig/model.py`, `README.md`,
`requirements.txt`) is written to that path under the output directory; directories are created. Files with
no extension, such as `Makefile`, cannot be produced this way. A trailing newline is added. A file chunk with no code produces an empty file. A chunk name that merely
*ends like* a file name (`step 2.1`) is taken for a file, so avoid such names.

Place file chunks together in an "Assembly" section at the end, with imports, boilerplate and `main`
guards in them, so the narrative chunks stay free of boilerplate.

## Figures: `#img`

`#img{`diagram.svg` The caption.}` is a figure: the path in a code span, then the caption as text. The path names
a file chunk of the program (a chunk named `diagram.svg` holding the SVG text, which tangle also writes out) or a
file next to the root `.lit`. It is not a chunk. `weave.py` inlines SVG into the HTML and embeds other images; for
LaTeX it converts SVG to a PDF with `rsvg-convert` or `inkscape` if one is installed, and copies PNG and JPEG.
`lint.py` reports a figure that cannot be found. Draw diagrams in SVG with `currentColor` for lines and text, so they
suit both light and dark pages and print black on white.

## Chapters: `#include`

A large program can be split over several files. `#include{path}` anywhere in a `.lit`, on a line of its own
or inside a bag, is replaced by the text of that file; the path is relative to the file that contains the
directive, and chapters may include further chapters.

    #h1{The Compiler}
    Overview of the whole thing.
    #include{lexer.lit}
    #include{parser/parser.lit}

- The tools see one text, the root with every chapter spliced in, so chunk names stay global: a chunk
  defined in one chapter may be referenced from another, and the same name in two chapters concatenates.
- Each chapter must be balanced on its own (every `#tag{` closed inside the same file), because a chapter
  is spliced in as it is. A chapter can start with its own `#h1{...}`: the first `h1` of the whole text is
  the title, later ones are chapters in the woven page.
- Each file may be included once (an error otherwise, naming the file); cycles are errors; a missing file
  is an error with the file and line of the directive. Every message names the chapter file and its own
  line, never a line of the spliced text.
- `#include` is reserved like `h1`; it cannot be a chunk name.
- Tools that take a `.lit` take the root. `python3 scripts/tangle.py book.lit out` writes the files; `absorb.py` and
  `sync.py` write only the chapter files whose text changes; `addr.py` accepts and prints
  `chapter.lit:LINE:COL` addresses (a bare line number means a line of the root); `watch.py` watches every
  chapter; `weave.py` and `lint.py` treat the whole as one document.
- Lines inserted exactly at the seam between two chapters are placed at the end of the earlier one.

## Text, math, lists, headings

- Paragraphs are separated by blank lines. Straight double quotes become typographic quotes in the output.
- Inline code: one backtick each side. If the content is `$...$` it is LaTeX math. Display math: a paragraph consisting of a
  single such span. Use plain amsmath/amssymb LaTeX; MathJax renders it in HTML, LaTeX uses it unchanged.
  `\tfrac`, `\mathrm`, `\mathcal`, `\operatorname`, `\sum`, `\frac`, `\big(` are all fine. Do not rely on custom macros.
- A paragraph whose lines start with `- ` is a bullet list; a line that does not start with `- ` continues the
  previous item.
- `#h1{Title}` first in the file is the document title. Further `h1` and `h2` to `h6` are sections (the
  shallowest level used becomes the top section level in LaTeX). Put headings at top level,
  between chunks; they appear in the table of contents.

## Citations

`#cite{key}` inline, or `#cite{key1,key2}`, or adjacent `#cite{a} #cite{b}` (rendered together as `[1, 2]`).
Numbers follow first appearance. The `#bibliography{}` bag holds one bag per key, each containing a fenced
BibTeX entry (fields read: author, title, journal/booktitle/institution/publisher, volume, number, pages, year,
note with `arXiv:ID`, howpublished with `\url{...}`, doi, isbn). More in `citations.md`.

## What the tools do with it

| tool | uses |
|---|---|
| tangle | chunks, fences, references, file names; writes files + sourcemap |
| weave | everything above plus headings, prose, math, lists, citations, bibliography; HTML + LaTeX |
| lint | all of it, to find structural and bibliographic mistakes before delivery |
| absorb, addr | the sourcemap, which requires the `.lit` to use LF line endings |

Syntax highlighting in the woven output follows the extension of the first file that (transitively)
uses a chunk; `.md` and `.txt` are not highlighted.

## Errors and fixes

| message (from tangle, lint, weave) | meaning | fix |
|---|---|---|
| `bag not closed (near end of file)` | some `#tag{` has no closing `}` | find the chunk you edited last; a `}` was deleted or hidden inside a code span you opened by accident |
| `no end tag "```" (near line N)` | a code fence is not closed, or code contains the fence delimiter | close it; use four backticks for the outer fence if the code has three |
| `a stray '}' in prose closed the document early, near line N` | an unmatched `}` in prose | wrap it in backticks or reword |
| `suspicious bag tag '...': a stray '#' in prose?` | a `#` outside backticks swallowed text up to the next `{` | wrap the `#` text in backticks |
| `undefined chunk <<<x>>>` / `<<<x>>> is referenced but no chunk ...` | typo in a reference or missing chunk | fix the name (names are case-sensitive and must match exactly) |
| `recursive chunk: a -> b -> a` | a chunk expands itself | restructure |
| `warning: chunk 'x' is never used` | defined, not referenced, not a file | reference it, or delete it |
| `#cite{k} has no bibliography entry` | key typo, or entry missing | add or fix the entry |
| `carriage returns found; convert the .lit to LF` | CRLF line endings | `sed -i 's/\r$//' file.lit` |
| `code outside any chunk is ignored` | a fence at top level | put it in a chunk |
