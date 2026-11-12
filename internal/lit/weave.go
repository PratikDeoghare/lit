package lit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var weaveSpec = Spec{
	Opts:  map[string]bool{"outdir": true, "only": true, "mathjax": true, "highlight": false},
	Short: map[string]string{"o": "outdir"},
}

// Weave turns a program into interlinked HTML (MathJax) and LaTeX.
func Weave(argv []string, io IO) int {
	a, err := ParseArgs(argv, weaveSpec)
	if err != nil || len(a.Pos) != 1 {
		fmt.Fprintln(io.Err, "usage: lit weave SOURCE.lit [-o OUTDIR] [--only html|tex] [--mathjax URL]")
		return 2
	}
	only := a.Get("only", "")
	if only != "" && only != "html" && only != "tex" {
		fmt.Fprintln(io.Err, "weave: --only is html or tex")
		return 2
	}
	src, err := ReadSource(a.Pos[0])
	if err != nil {
		fmt.Fprintln(io.Err, "weave: "+err.Error())
		return 2
	}
	root, err := src.Parse()
	if err != nil {
		fmt.Fprintln(io.Err, "weave: "+err.Error())
		return 2
	}
	name := filepath.Base(a.Pos[0])
	doc := NewDoc(root, name, src.Base, io.Err)
	outdir := a.Get("outdir", ".")
	if err := os.MkdirAll(outdir, 0o755); err != nil {
		fmt.Fprintln(io.Err, "weave: "+err.Error())
		return 2
	}
	stem := filepath.Join(outdir, strings.TrimSuffix(name, filepath.Ext(name)))
	if only != "tex" {
		w := &htmlWeaver{doc, a.Get("mathjax", mathJax)}
		if err := os.WriteFile(stem+".html", []byte(w.render()), 0o644); err != nil {
			fmt.Fprintln(io.Err, "weave: "+err.Error())
			return 2
		}
		fmt.Fprintf(io.Out, "wrote %s.html  (%d chunks, %d citations)\n", stem, len(doc.defs), len(doc.citeNo))
	}
	if only != "html" {
		w := newTexWeaver(doc, outdir, func(s string) { fmt.Fprint(io.Err, s) })
		if err := os.WriteFile(stem+".tex", []byte(w.render()), 0o644); err != nil {
			fmt.Fprintln(io.Err, "weave: "+err.Error())
			return 2
		}
		fmt.Fprintf(io.Out, "wrote %s.tex   -> compile with:  xelatex %s.tex   (twice; pdflatex cannot read all of Unicode)\n", stem, filepath.Base(stem))
	}
	return 0
}
