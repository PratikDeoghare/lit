package lit

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const longCode = 40 // lines of code in one chunk, without any prose, before we warn

var lintSpec = Spec{Opts: map[string]bool{"refs": false}}

// Lint runs the pre-flight checks on a literate source. Errors give exit status 1; warnings do not.
func Lint(argv []string, io IO) int {
	a, err := ParseArgs(argv, lintSpec)
	if err != nil || len(a.Pos) != 1 {
		fmt.Fprintln(io.Err, "usage: lit lint SOURCE.lit [--refs]")
		return 2
	}
	src, err := ReadSource(a.Pos[0])
	if err != nil {
		fmt.Fprintln(io.Err, "lint: "+err.Error())
		return 2
	}
	root, err := src.Parse()
	if err != nil {
		fmt.Fprintln(io.Err, "lint: "+err.Error())
		return 2
	}
	return lintDoc(NewDoc(root, filepath.Base(a.Pos[0]), src.Base, nil), a.Has("refs"), io.Out)
}

func wordCount(it *item) int {
	text, _ := runText(it.kids)
	return len(strings.Fields(strings.Map(func(r rune) rune {
		if r >= 0xe000 && r <= 0xe003 {
			return ' '
		}
		return r
	}, text)))
}

func lintDoc(doc *Doc, showRefs bool, out io.Writer) int {
	var errs, warns []string
	for _, p := range walk(doc.root) {
		if p.it.kind == "img" {
			if path, _ := imgParts(p.it.bag); doc.image(path) == nil {
				errs = append(errs, fmt.Sprintf("#img{%s}: neither a file chunk of this document nor a file next to it", path))
			}
		}
	}
	type stats struct{ code, prose int }
	per := map[string]*stats{}
	get := func(n string) *stats {
		if per[n] == nil {
			per[n] = &stats{}
		}
		return per[n]
	}
	codeLines, proseWords := 0, 0
	for _, p := range walk(doc.root) {
		switch {
		case p.it.kind == "code" && p.c.name != "":
			get(p.c.name).code += len(p.it.lines)
			codeLines += len(p.it.lines)
		case p.it.kind == "prose":
			w := wordCount(p.it)
			if p.c.name != "" {
				get(p.c.name).prose += w
			}
			proseWords += w
		}
	}
	for _, name := range doc.defOrder {
		s := per[name]
		if s == nil {
			s = &stats{}
		}
		isFile := fileRe.MatchString(name)
		if _, used := doc.uses[name]; !used && !isFile && s.code > 0 {
			warns = append(warns, fmt.Sprintf("chunk «%s» has code but is never referenced, so it is never written to a file", name))
		}
		if s.code == 0 && s.prose == 0 && !isFile {
			warns = append(warns, fmt.Sprintf("chunk «%s» is empty", name))
		}
		if s.code > longCode && s.prose == 0 {
			warns = append(warns, fmt.Sprintf("chunk «%s» has %d lines of code and no prose; split it into named steps and explain them", name, s.code))
		}
	}
	for _, name := range doc.useOrder {
		if _, ok := doc.defs[name]; !ok {
			errs = append(errs, fmt.Sprintf("<<<%s>>> is referenced but no chunk of that name is defined", name))
		}
	}
	for _, key := range doc.citeOrder {
		if _, ok := doc.bibsrc[key]; !ok {
			errs = append(errs, fmt.Sprintf("#cite{%s} has no entry in #bibliography{}", key))
		}
	}
	type row struct{ key, authors, year, title, ident string }
	var rows []row
	for _, key := range doc.bibOrder {
		fields := parseBibtex(doc.bibsrc[key])
		if _, cited := doc.citeNo[key]; !cited {
			warns = append(warns, fmt.Sprintf("bibliography entry '%s' is never cited", key))
		}
		var missing []string
		for _, f := range []string{"author", "title", "year"} {
			if strings.TrimSpace(fields[f]) == "" {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			errs = append(errs, fmt.Sprintf("bibliography entry '%s' lacks: %s", key, strings.Join(missing, ", ")))
		}
		p := bibParts(fields)
		ident := ""
		switch {
		case p.arxiv != "":
			ident = "arXiv:" + p.arxiv
		case fields["doi"] != "":
			ident = "doi:" + fields["doi"]
		case fields["isbn"] != "":
			ident = "isbn:" + fields["isbn"]
		default:
			ident = p.url
		}
		if ident == "" {
			warns = append(warns, fmt.Sprintf("bibliography entry '%s' has no arXiv id, DOI, ISBN or URL; check it by title and authors", key))
			ident = "(no identifier)"
		}
		rows = append(rows, row{key, p.authors, p.year, p.title, ident})
	}
	// hints for the agent that are still open are worth knowing about
	for _, p := range walk(doc.root) {
		if p.it.kind == "hint" {
			warns = append(warns, fmt.Sprintf("an agent hint is still open in «%s»: %s", p.c.name, trunc(p.it.text, 60)))
		}
	}
	if showRefs {
		fmt.Fprintln(out, "references to verify (author, year, title, identifier):")
		for _, r := range rows {
			fmt.Fprintf(out, "  %-28s %-46s %s  %s  %s\n", r.key, trunc(r.authors, 46), r.year, trunc(r.title, 70), r.ident)
		}
		fmt.Fprintln(out)
	}
	for _, w := range warns {
		fmt.Fprintln(out, "warning:", w)
	}
	for _, e := range errs {
		fmt.Fprintln(out, "error:", e)
	}
	fmt.Fprintf(out, "%d chunks, %d code lines, %d words of prose, %d citations, %d bibliography entries; %d error(s), %d warning(s)\n",
		len(doc.defs), codeLines, proseWords, len(doc.citeNo), len(doc.bibsrc), len(errs), len(warns))
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
