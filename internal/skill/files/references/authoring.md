# Authoring a literate program

Contents: what makes one good · narrative skeletons · chunking and naming · writing the prose ·
before and after · assembly, imports, Python indentation · converting existing code · tests ·
long documents · maintaining · checklist

## What makes one good

A literate program is read by a person who wants to understand the program, then the code. Three
things decide whether it succeeds:

1. **The order serves the reader.** Concepts come before the code that needs them. The compiler's order
   is irrelevant; chunks and references remove that constraint.
2. **The prose carries information the code cannot.** Why this approach, what the invariant is, what
   it costs, what was rejected, where the idea comes from. Code already says what it does.
3. **Nothing is hidden.** Every line of code is shown somewhere, and the files are assembled
   mechanically, so what the reader sees is what runs.

## Narrative skeletons

Pick one and adapt it. Each part becomes an `#h2{...}` section; the assembly section comes last.

- **Algorithm or data structure:** the problem and why it is hard, the key idea, an invariant or recurrence in
  math, the core procedure in named steps, edge cases, complexity, tests, history and references.
- **Library or module:** purpose and a usage example first, the data model, the public functions
  (each with its contract), internals, error handling, tests, assembly.
- **Research-paper implementation:** the paper's claim in two sentences, notation mapped to code names, model
  components in the order the paper introduces them (each with the equation it implements and a
  citation), data, training loop, evaluation, ablations or knobs, reproduction notes, assembly.
- **Command-line tool or script:** what it does for the user, input and output, the pipeline stages in
  order, option handling, failure modes, assembly.
- **Data pipeline:** sources and their licences, schema, each transformation with the reason for
  it, validation, outputs.

Open with a short overview that tells the reader what they will understand by the end, and name
the files that come out of it.

## Chunking and naming

- One idea per chunk, typically 3 to 30 lines. If you cannot say what the chunk achieves in one phrase, split or merge it.
- Name by purpose. Noun phrases for definitions (`merge`, `learning-rate schedule`), verb phrases for
  steps (`take the smaller head`, `clip the gradient`). Avoid `part 1`, `helper`, `misc`. Names are
  how the reader navigates the index, so make them read well in `<<<angle brackets>>>`.
- Break a long function into named steps and let the parent chunk show the function's shape with
  references. Do not split in the middle of an expression just to hit a size.
- Group related small chunks under one heading and one explanation; a chunk may hold several fenced
  blocks with prose between them if that reads better, and they concatenate in order.
- Do not reference the same chunk from several places unless duplication is intended. A chunk is one text.
- Keep boilerplate (imports, `if __name__ == "__main__"`, license headers) in the file chunks
  of the assembly section.
- Nest a chunk inside its parent when it exists only to serve that parent; keep it top level when other
  chunks use it too.

## Writing the prose

Do:

- Start each chunk with the point: what problem this piece solves and the approach taken.
- State invariants, preconditions and complexity in math where it clarifies:
  `` `$T(n) = 2T(n/2) + \Theta(n)$` ``. Define every symbol, and say which code name carries it.
- Say why a choice was made over the obvious alternative, and what it costs.
- Mention the failure case you had to handle and what happens without the fix.
- Cite the source of any non-obvious technique, formula, constant or dataset where it is first used.
- Keep paragraphs short and concrete. Prefer one precise sentence to three vague ones.

Do not:

- Restate the code in English ("we set `i` to zero, then loop while `i` is less than ...").
- Apologise, hedge, or pad. If you are unsure about a claim, check it or leave it out.
- Make claims about performance or correctness that you did not verify.
- Leave a chunk of more than a screenful of code with no prose.

## Before and after

Code to explain:

```python
def softmax(x):
    z = x - x.max(axis=-1, keepdims=True)
    e = np.exp(z)
    return e / e.sum(axis=-1, keepdims=True)
```

Weak (narrates the code, adds nothing):

> We subtract the max from x, call exp, then divide by the sum.

Strong (intent, reason, consequence):

> The softmax of a row is `$e^{x_i} / \sum_j e^{x_j}$`. Evaluated directly, `exp` overflows for inputs
> above about 709 in double precision. Subtracting the row maximum first changes nothing mathematically, because the
> factor `$e^{-m}$` cancels between numerator and denominator, and it keeps every exponent at most zero, so the
> largest term is exactly 1.

The strong version is also checkable: the claim about overflow can be tested in two lines, and the
cancellation is a one-line argument the reader can follow.

## Assembly, imports, Python indentation

- Put imports and boilerplate in the file chunk, then references to the narrative chunks:
  ````
  #mergesort.py{
    ```
    """Module docstring."""
    import math


    <<<merge>>>


    <<<merge sort>>>
    ```
  }
  ````
- Put two blank lines between top-level definitions by leaving two blank lines between references in the file chunk.
- Write each chunk body from column 0. The reference's indentation re-indents it, so a chunk
  referenced inside a function body, a class, or a loop lands at the right depth. Never indent a chunk body to
  "match" where it will be used.
- A method chunk referenced inside a class: reference it at the class-body indentation
  (four spaces) in the class chunk, with a blank line between methods.
- A chunk that is used inside a class and a module level at different indentation will be wrong in one of
  them; split it.
- Tabs and spaces: use spaces throughout the `.lit` so the dedent logic and indentation add up.

## Converting existing code

1. Copy the original files to a safe place and run them (or their tests) to record the behaviour.
2. Read all of it. Find the seams: the functions, the stages, the data definitions.
3. Draft the narrative (skeleton above) and decide the chunks. Keep every original line, in the same
   relative order within a function. Original comments may stay as code comments if they help locally;
   explanations belong in prose.
4. Write the file chunks so that the tangled files equal the originals. Compare with
   `diff -r original out` (allow `-B -w` only if you deliberately normalised whitespace).
5. Run the tangled code. If behaviour differs, the `.lit` is wrong.
6. If you find bugs or have improvements, leave the code as it was, say so in the prose, and report them
   to the user. Changing behaviour is a separate decision.

## Tests, examples, configuration

Tests are first-class: give them a section, explain which properties they check and why those
suffice, and tangle them into their own file chunk. Put usage examples and expected output in prose
or in a README chunk. Configuration, requirements and build files are ordinary file chunks.

## Long documents

Use `#h2{Part I: ...}` for parts and `#h3{...}` inside them. Keep the order: overview, parts, assembly.
Use the top-level prose between chunks for transitions. If a section needs a table of definitions, a list,
or a figure described in text, do that in prose; the notation has no tables or images.

## Splitting into chapters

When one file becomes hard to hold in your head (a few thousand lines, or several subsystems), make the
root file the overview and give each module or subsystem its own chapter file:

    #h1{Title}
    Overview: what the program is for, how the parts fit, and the order they are explained in.
    #include{storage.lit}
    #include{network/protocol.lit}
    #include{assembly.lit}

Each chapter starts with `#h1{Chapter title}` and reads as its own short literate program: purpose,
data structures, the chunks. Name chunks so they stay unambiguous across chapters (`storage: open file`
rather than `open file`). Chapters may refer to chunks of other chapters, but say so in prose with a
sentence of context, because the reader may arrive from the index. Put file chunks that assemble
several modules into an `assembly.lit` chapter. See `references/notation.md` (Chapters) for the rules.
Convert an existing codebase one module at a time: one chapter per module, tangle, diff against the
original files, then continue.

## Maintaining

Edit the `.lit` for anything that is more than a tweak; use `absorb.py` to fold small direct edits of generated files
back in. Keep chunk names stable. Re-run `lint.py`, tangle, run the tests, and weave after each change. Commit the
`.lit`; generated files and the `.map` can be committed or ignored, but the `.lit` is the truth.

## Checklist before delivering

- `lint.py` reports no errors, and the warnings are understood.
- Tangled files compile, run, and (for conversions) match the originals.
- Each chunk has prose explaining why, no chunk is a bare wall of code.
- Every formula has its symbols defined; every claim that needed a source has a verified citation.
- Every claim about behaviour (overflow, error messages, complexity, measured rates) was run or derived, not assumed.
- References verified or clearly flagged; no pasted passages from papers.
- Woven HTML opens with math rendered; LaTeX compiles if it was requested.
- The summary tells the user what was verified, what was not, and what was left unchanged.
