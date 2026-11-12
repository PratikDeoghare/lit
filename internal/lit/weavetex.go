package lit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var texSpecial = map[rune]string{'\\': `\textbackslash{}`, '{': `\{`, '}': `\}`, '$': `\$`, '&': `\&`, '#': `\#`, '_': `\_`,
	'%': `\%`, '~': `\textasciitilde{}`, '^': `\textasciicircum{}`, '<': `\textless{}`, '>': `\textgreater{}`, '|': `\textbar{}`}

func texEsc(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if t, ok := texSpecial[r]; ok {
			sb.WriteString(t)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

var breakRe = regexp.MustCompile(`(\\_|/|\.)`)

// texCodeEsc escapes for \texttt and allows line breaks after _ / .
func texCodeEsc(s string) string {
	return breakRe.ReplaceAllStringFunc(texEsc(s), func(m string) string { return m + `\allowbreak{}` })
}

func verbEsc(s string) string {
	return strings.NewReplacer(`\`, `\textbackslash{}`, "{", `\{`, "}", `\}`).Replace(s)
}

type texWeaver struct {
	d        *Doc
	outdir   string
	lab      map[string]string
	minLevel int
	warn     func(string)
}

func newTexWeaver(d *Doc, outdir string, warn func(string)) *texWeaver {
	w := &texWeaver{d: d, outdir: outdir, lab: map[string]string{}, warn: warn}
	for _, cs := range d.defs {
		for _, c := range cs {
			w.lab[c.id] = fmt.Sprintf("chunk%d", c.num)
		}
	}
	w.minLevel = 0
	for _, h := range d.heads {
		if !(h.level == 1 && h.title == d.title) && (w.minLevel == 0 || h.level < w.minLevel) {
			w.minLevel = h.level
		}
	}
	if w.minLevel == 0 {
		w.minLevel = 1
	}
	return w
}

func (w *texWeaver) label(name string) string {
	if fileRe.MatchString(name) {
		return `\texttt{` + texCodeEsc(name) + `}`
	}
	return `\chunkname{` + texCodeEsc(name) + `}`
}

func (w *texWeaver) citeTex(m string) string {
	var known, out []string
	keys := citeKeys(m)
	for _, k := range keys {
		if _, ok := w.d.bibsrc[k]; ok {
			known = append(known, k)
		}
	}
	s := ""
	if len(known) > 0 {
		s = `\cite{` + strings.Join(known, ",") + `}`
	}
	for _, k := range keys {
		if _, ok := w.d.bibsrc[k]; !ok {
			out = append(out, `\textbf{?`+texEsc(k)+`}`)
		}
	}
	return s + strings.Join(out, "")
}

func codeSpanTex(k Code) string {
	if tex, ok := mathOf(k); ok {
		return `\(` + tex + `\)`
	}
	return `\texttt{` + texCodeEsc(k.Text()) + `}`
}

func (w *texWeaver) fill(s string, table []Code) string {
	s = quotes.ReplaceAllString(texEsc(s), "``$1''")
	s = citeRun.ReplaceAllStringFunc(s, w.citeTex)
	return subPlaceholders(s, table, codeSpanTex)
}

func (w *texWeaver) inline(kids []Node) string {
	text, table := runText(kids)
	return w.fill(strings.TrimSpace(text), table)
}

func (w *texWeaver) prose(kids []Node) string {
	blocks, table := w.d.blocks(kids)
	var out []string
	for _, b := range blocks {
		switch b.kind {
		case "ul":
			var items []string
			for _, i := range b.items {
				items = append(items, `\item `+w.fill(i, table))
			}
			out = append(out, "\\begin{itemize}\n"+strings.Join(items, "\n")+"\n\\end{itemize}")
		case "math":
			out = append(out, "\\[\n"+b.text+"\n\\]")
		default:
			out = append(out, w.fill(b.text, table))
		}
	}
	return strings.Join(out, "\n\n")
}

func (w *texWeaver) code(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	d := w.d
	var segs, buf []string
	for _, ln := range lines {
		if m := refRe.FindStringSubmatch(ln); m != nil {
			if len(buf) > 0 {
				segs = append(segs, verbEsc(strings.Join(buf, "\n")))
				buf = nil
			}
			name := strings.TrimSpace(m[2])
			if cs, ok := d.defs[name]; ok {
				segs = append(segs, m[1]+`\chunkrefv{`+w.lab[cs[0].id]+`}`)
			} else {
				segs = append(segs, m[1]+`\textbf{??}`)
			}
		} else {
			buf = append(buf, Unescape(ln))
		}
	}
	if len(buf) > 0 {
		segs = append(segs, verbEsc(strings.Join(buf, "\n")))
	}
	return "\\begin{Verbatim}\n" + strings.Join(segs, "\n") + "\n\\end{Verbatim}"
}

func (w *texWeaver) link(c *chunk) string {
	return `\chunklink{` + w.lab[c.id] + `}{` + w.label(c.name) + `}`
}

func (w *texWeaver) meta(c *chunk) string {
	d := w.d
	var bits []string
	if users := d.users(c); len(users) > 0 {
		var l []string
		for _, u := range users {
			l = append(l, w.link(u))
		}
		bits = append(bits, "used in "+strings.Join(l, ", "))
	}
	if !fileRe.MatchString(c.name) && len(d.partof[c.name]) > 0 {
		var l []string
		for _, f := range d.partof[c.name] {
			l = append(l, `\hyperref[`+w.lab[d.defs[f][0].id]+`]{\texttt{`+texCodeEsc(f)+`}}`)
		}
		bits = append(bits, "part of "+strings.Join(l, ", "))
	}
	if len(bits) == 0 {
		return ""
	}
	return `\chunkmeta{` + strings.Join(bits, ` \textperiodcentered{} `) + `}`
}

func (w *texWeaver) chunk(c *chunk, depth int) string {
	var title string
	if fileRe.MatchString(c.name) {
		title = `\filebadge\ \textbf{\texttt{` + texCodeEsc(c.name) + `}}`
	} else {
		title = `\textbf{\ensuremath{\langle}\texttt{` + texCodeEsc(c.name) + `}\ensuremath{\rangle}}\enspace\ensuremath{\equiv}`
	}
	var parts []string
	if depth > 0 {
		parts = append(parts, `\begin{nested}`)
	}
	parts = append(parts, `\chunkhead{`+w.lab[c.id]+`}{`+title+`}`, w.meta(c))
	for _, it := range c.items {
		switch it.kind {
		case "prose":
			parts = append(parts, w.prose(it.kids))
		case "code":
			parts = append(parts, w.code(it.lines))
		case "chunk":
			parts = append(parts, w.chunk(it.chunk, depth+1))
		case "head":
			parts = append(parts, w.heading(it))
		case "img":
			parts = append(parts, w.figure(it))
		case "hint":
			parts = append(parts, w.hint(it))
		}
	}
	if depth > 0 {
		parts = append(parts, `\end{nested}`)
	}
	return joinNonEmpty(parts, "\n")
}

func (w *texWeaver) hint(it *item) string {
	return `{\small\itshape to be filled in by an agent: ` + texEsc(it.text) + `\par}`
}

func joinNonEmpty(parts []string, sep string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func (w *texWeaver) heading(it *item) string {
	d := w.d
	hid := d.headID[it]
	depth := min(it.level-w.minLevel, 3)
	cmds := []string{"section", "subsection", "subsubsection", "paragraph"}
	cmd := cmds[max(depth, 0)]
	return `\hypertarget{` + hid + `}{}\phantomsection\addcontentsline{toc}{` + cmd + `}{` + texEsc(d.headTitle[hid]) + `}` +
		"\n" + `\` + cmd + `*{` + w.inline(it.kids) + `}`
}

// figure writes a figure environment; the picture is written beside the .tex as a PDF (or copied, if raster).
func (w *texWeaver) figure(it *item) string {
	path, cap := imgParts(it.bag)
	f := w.d.image(path)
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(filepath.Base(path), ext)
	target := ""
	if f != nil {
		os.MkdirAll(w.outdir, 0o755)
		if strings.ToLower(ext) == ".svg" {
			tool, _ := exec.LookPath("rsvg-convert")
			if tool == "" {
				tool, _ = exec.LookPath("inkscape")
			}
			if tool != "" {
				tmp, err := os.MkdirTemp("", "lit-fig")
				if err == nil {
					defer os.RemoveAll(tmp)
					src := filepath.Join(tmp, stem+".svg")
					text := f.val
					if f.kind == "file" {
						b, _ := os.ReadFile(f.val)
						text = string(b)
					}
					os.WriteFile(src, []byte(text), 0o644)
					out := filepath.Join(w.outdir, stem+".pdf")
					var cmd *exec.Cmd
					if strings.HasSuffix(tool, "rsvg-convert") {
						cmd = exec.Command(tool, "-f", "pdf", "-o", out, src)
					} else {
						cmd = exec.Command(tool, src, "--export-type=pdf", "--export-filename="+out)
					}
					if cmd.Run() == nil {
						target = stem + ".pdf"
					}
				}
			}
			if target == "" {
				w.warn(fmt.Sprintf("note: %s: no rsvg-convert or inkscape, so the figure is left out of the LaTeX\n", path))
			}
		} else {
			target = filepath.Base(path)
			data := []byte(f.val)
			if f.kind == "file" {
				data, _ = os.ReadFile(f.val)
			}
			os.WriteFile(filepath.Join(w.outdir, target), data, 0o644)
		}
	}
	pic := `\fbox{\texttt{` + texCodeEsc(path) + `} (figure not available)}`
	if target != "" {
		pic = `\includegraphics[width=\linewidth]{` + target + `}`
	}
	s := "\\begin{figure}[htbp]\n\\centering\n" + pic + "\n"
	if cap != "" {
		s += `{\small\itshape ` + texEsc(cap) + `\par}` + "\n"
	}
	return s + "\\end{figure}"
}

func (w *texWeaver) bibliography() string {
	d := w.d
	var items []string
	for _, key := range d.citeOrder {
		src, ok := d.bibsrc[key]
		if !ok {
			continue
		}
		p := bibParts(parseBibtex(src))
		var parts []string
		if p.authors != "" {
			parts = append(parts, stop(texEsc(p.authors)))
		}
		parts = append(parts, "``"+stop(texEsc(p.title))+"''")
		var tail []string
		if strings.TrimSpace(p.venue) != "" {
			tail = append(tail, `\emph{`+texEsc(p.venue)+`}`)
		}
		for _, x := range []string{texEsc(p.vol), texEsc(p.pages), texEsc(p.year)} {
			if x != "" {
				tail = append(tail, x)
			}
		}
		if len(tail) > 0 {
			parts = append(parts, strings.Join(tail, ", ")+".")
		}
		if p.url != "" {
			parts = append(parts, `\url{`+p.url+`}`)
		} else if p.hp != "" {
			parts = append(parts, stop(texEsc(p.hp)))
		}
		if p.arxiv != "" {
			parts = append(parts, `\href{https://arxiv.org/abs/`+p.arxiv+`}{arXiv:`+p.arxiv+`}`)
		}
		var back []string
		for _, r := range d.citedIn[key] {
			back = append(back, w.linkBack(r.id, r.name))
		}
		items = append(items, `\bibitem{`+key+`} `+strings.Join(parts, " ")+"\n"+
			`\par{\footnotesize\color{tuftegray}cited in `+strings.Join(back, ", ")+`}`)
	}
	return "\\clearpage\n\\phantomsection\\addcontentsline{toc}{section}{Bibliography}\n" +
		"\\renewcommand{\\refname}{Bibliography}\n\\begin{thebibliography}{99}\n" + strings.Join(items, "\n") + "\n\\end{thebibliography}"
}

func (w *texWeaver) linkBack(id, name string) string {
	if strings.HasPrefix(id, "c-") {
		return `\hyperref[` + w.lab[id] + `]{` + w.label(name) + `}`
	}
	return `\hyperlink{` + id + `}{` + texEsc(name) + `}`
}

func (w *texWeaver) index() string {
	d := w.d
	var rows []string
	for _, name := range sortedNames(d) {
		c := d.defs[name][0]
		var us []string
		for _, u := range d.users(c) {
			us = append(us, `\hyperref[`+w.lab[u.id]+`]{\ref*{`+w.lab[u.id]+`}}`)
		}
		rows = append(rows, `\hyperref[`+w.lab[c.id]+`]{`+w.label(name)+`} & \ref*{`+w.lab[c.id]+`} & `+strings.Join(us, ", ")+` \\`)
	}
	return "\\clearpage\n\\hypertarget{index}{}\\phantomsection\\addcontentsline{toc}{section}{Chunk index}\n" +
		"\\section*{Chunk index}\n\\begin{longtable}{@{}p{0.40\\textwidth}rp{0.42\\textwidth}@{}}\n" +
		"\\textbf{chunk} & \\textbf{defined} & \\textbf{used in} \\\\\\hline\n\\endhead\n" + strings.Join(rows, "\n") +
		"\n\\end{longtable}"
}

func (w *texWeaver) render() string {
	d := w.d
	var names []string
	for _, n := range d.defOrder {
		names = append(names, `\expandafter\gdef\csname cn@`+w.lab[d.defs[n][0].id]+`\endcsname{`+texCodeEsc(n)+`}`)
	}
	r := strings.NewReplacer("@@STYLE@@", "", "@@TITLE@@", texEsc(d.title), "@@PDFTITLE@@", texEsc(d.title),
		"@@SOURCE@@", texCodeEsc(d.srcName), "@@NAMES@@", strings.Join(names, "\n"))
	head := r.Replace(texPreamble)
	var body []string
	firstH1 := true
	for _, it := range d.root.items {
		switch it.kind {
		case "head":
			if it.level == 1 && firstH1 {
				firstH1 = false
			} else {
				body = append(body, w.heading(it))
			}
		case "prose":
			body = append(body, w.prose(it.kids))
		case "code":
			body = append(body, w.code(it.lines))
		case "chunk":
			body = append(body, w.chunk(it.chunk, 0))
		case "img":
			body = append(body, w.figure(it))
		case "hint":
			body = append(body, w.hint(it))
		}
	}
	body = append(body, w.index(), w.bibliography(), `\end{document}`)
	return head + joinNonEmpty(body, "\n\n") + "\n"
}
