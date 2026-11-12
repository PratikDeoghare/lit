# References: finding, verifying, formatting

Contents: why this needs care · what deserves a citation · finding and verifying · BibTeX the tools read ·
wording and placing citations · when you cannot search · reporting

## Why this needs care

A reference that looks right and is wrong (a plausible title, a wrong year, an author who did not write
it, an arXiv number that belongs to another paper) is worse than no reference: it is believed. Language
models are especially prone to producing exactly that from memory. So the rule is: **a reference goes in the
bibliography only after it has been checked against a source that you actually looked at in this session, or it is
clearly flagged as unchecked.**

## What deserves a citation

- The origin of an algorithm, technique, architecture, loss, optimiser or trick that the code implements.
- A formula, constant or bound taken from a paper or textbook.
- A design choice justified by evidence ("pre-norm trains more stably").
- A dataset, benchmark or standard the program depends on.
- A library or tool whose behaviour the explanation relies on (cite its paper if it has one, else its documentation URL).
- Background a curious reader should follow next: the survey, the textbook chapter, the canonical paper.

Do not cite for common knowledge, and do not decorate a sentence with citations to look scholarly. As a
rule of thumb, one to three per major idea; a dozen for a research-paper implementation, not hundreds.

## Finding and verifying

For each candidate reference, do all of the following with whatever tools you have:

1. **Search** for the title or the idea (for example `web_search` with the title in quotes, or the technique
   name plus "paper"). Prefer the primary source over a blog or an aggregator.
2. **Open the source** (`web_fetch` the arXiv abstract page `https://arxiv.org/abs/<id>`, the publisher page,
   the proceedings page, DBLP or the ACL Anthology) and read title, full author list, venue and year.
3. **Match the claim.** Check that the paper says what you attribute to it. Skim the abstract and the
   relevant section; do not rely on the title.
4. **Pick the right version.** Use the published venue and year when there is one, and add the arXiv id in `note`.
   For arXiv-only work use the year of first submission.
5. **Record an identifier**: arXiv id, DOI, ISBN or URL. `lint.py` warns when an entry has none because then
   nothing can verify it.
6. **Leave out what you cannot confirm.** Omit a field (pages, volume, DOI) rather than guess it.

Books: confirm title, edition, publisher, year and ISBN from the publisher or a library catalogue page.
Classic papers without arXiv ids (1970s journals, for instance): confirm journal, volume, pages and year from the
publisher or a reliable index, and give the DOI when it exists.

If two sources disagree, trust the venue's own page and mention nothing you cannot settle.

## BibTeX the tools read

Each entry goes in its own bag under `#bibliography{}`, inside a fenced block. The bag tag, the BibTeX key
and the `#cite{}` key are the same string. Convention: `firstauthorYEARword` in lower case
(`vaswani2017`, `touvron2023llama`).

````
#bibliography{
  #vaswani2017{
    ```
    @inproceedings{vaswani2017,
      author    = {Ashish Vaswani and Noam Shazeer and Niki Parmar and Jakob Uszkoreit and Llion Jones and Aidan N. Gomez and Lukasz Kaiser and Illia Polosukhin},
      title     = {Attention Is All You Need},
      booktitle = {Advances in Neural Information Processing Systems},
      year      = {2017},
      note      = {arXiv:1706.03762}
    }
    ```
  }
}
````

Fields read: `author` (names joined with `and`; `and others` becomes "et al."), `title`, one of `journal`,
`booktitle`, `institution`, `publisher` (shown as the venue), `volume`, `number`, `pages`, `year`, `note` containing
`arXiv:ID` (becomes a link), `howpublished = {\url{...}}` (a link), `doi`, `isbn`, `edition`. Required by `lint.py`: author,
title, year. Protect capitals with braces (`{GQA}`) and write accents as `{\'e}`. Examples by kind:

- Article: `@article{...}` with `journal`, `volume`, `number`, `pages`, `year`, `doi`.
- Conference: `@inproceedings{...}` with `booktitle`, `year`, `note = {arXiv:ID}` when there is one.
- Book: `@book{...}` with `publisher`, `edition`, `year`, `isbn`.
- Preprint only: `@article{...}` with `journal = {arXiv preprint arXiv:ID}`, `year`.
- Web page or software: `@misc{...}` with `howpublished = {\url{https://...}}`, `year`.

## Wording and placing citations

- Put `#cite{key}` right after the claim it supports, before the punctuation: "RoPE rotates query and key
  pairs #cite{su2021rope}." Several sources for one claim: `#cite{a} #cite{b}`.
- Paraphrase. Never paste sentences or paragraphs from a paper. A few words in quotes are fine when the
  exact wording matters.
- Say what the source established, in your own words, at the strength it was established: "reported",
  "showed", "proposed", not "proved" unless it did.
- Do not cite for details you added yourself, and do not attribute your own design choice to a paper.
- A formula taken from a paper gets the citation and, if the notation differs, a sentence mapping the
  paper's symbols to the code's names.

## When you cannot search

Without a search or fetch tool, cite only works you are certain exist and can state exactly: canonical
textbooks and landmark papers whose title, authors and year you know with confidence. Mark the
bibliography as unverified in your message to the user, give the list from `python3 scripts/lint.py prog.lit --refs`
as their checklist, and prefer fewer references. Never invent a DOI, page range or arXiv number.

## Reporting

End with a short section to the user:

- Verified: keys checked against a source, and which source.
- Unverified: keys written from memory, to be checked.
- Dropped: references you considered and could not confirm.
- Claims worth a second look: anywhere the paper may support less than the prose says.
