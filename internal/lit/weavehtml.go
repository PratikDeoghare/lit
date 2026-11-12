package lit

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	escText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	escAttr = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")
)

func esc(s string) string { return escText.Replace(s) }

type htmlWeaver struct {
	d       *Doc
	mathjax string
}

func (w *htmlWeaver) citeHTML(m string) string {
	d := w.d
	var links []string
	for _, k := range citeKeys(m) {
		if _, ok := d.bibsrc[k]; ok {
			links = append(links, fmt.Sprintf(`<a href="#bib-%s">%d</a>`, k, d.citeNo[k]))
		} else {
			links = append(links, "?"+esc(k))
		}
	}
	return `<span class="cite">[` + strings.Join(links, ", ") + "]</span>"
}

func mathHTML(tex string, display bool) string {
	if display {
		return `<div class="display">\[` + esc(tex) + `\]</div>`
	}
	return `<span class="math">\(` + esc(tex) + `\)</span>`
}

func codeSpanHTML(k Code) string {
	if tex, ok := mathOf(k); ok {
		return mathHTML(tex, false)
	}
	return "<code>" + esc(k.Text()) + "</code>"
}

func subPlaceholders(s string, table []Code, f func(Code) string) string {
	return codePH.ReplaceAllStringFunc(s, func(m string) string {
		var i int
		fmt.Sscan(codePH.FindStringSubmatch(m)[1], &i)
		return f(table[i])
	})
}

func (w *htmlWeaver) fill(s string, table []Code) string {
	s = quotes.ReplaceAllString(esc(s), "“$1”")
	s = citeRun.ReplaceAllStringFunc(s, w.citeHTML)
	return subPlaceholders(s, table, codeSpanHTML)
}

func (w *htmlWeaver) inline(kids []Node) string {
	text, table := runText(kids)
	return w.fill(strings.TrimSpace(text), table)
}

func (w *htmlWeaver) prose(kids []Node) string {
	blocks, table := w.d.blocks(kids)
	var out []string
	for _, b := range blocks {
		switch b.kind {
		case "ul":
			s := "<ul>"
			for _, i := range b.items {
				s += "<li>" + w.fill(i, table) + "</li>"
			}
			out = append(out, s+"</ul>")
		case "math":
			out = append(out, mathHTML(b.text, true))
		default:
			out = append(out, "<p>"+w.fill(b.text, table)+"</p>")
		}
	}
	return strings.Join(out, "\n")
}

func (w *htmlWeaver) refLink(name, indent string) string {
	c, ok := w.d.defs[name]
	if !ok {
		return esc(indent) + `<span class="ref bad">` + "«" + esc(name) + "»</span>"
	}
	return fmt.Sprintf(`%s<a class="ref pg" href="#%s">«%s»<span class="rn">§%d</span></a>`, esc(indent), c[0].id, esc(name), c[0].num)
}

func (w *htmlWeaver) code(lines []string) string {
	var segs, buf []string
	for _, ln := range lines {
		if m := refRe.FindStringSubmatch(ln); m != nil {
			if len(buf) > 0 {
				segs = append(segs, esc(strings.Join(buf, "\n")))
				buf = nil
			}
			segs = append(segs, w.refLink(strings.TrimSpace(m[2]), m[1]))
		} else {
			buf = append(buf, Unescape(ln))
		}
	}
	if len(buf) > 0 {
		segs = append(segs, esc(strings.Join(buf, "\n")))
	}
	return `<pre class="code">` + strings.Join(segs, "\n") + "</pre>"
}

func label(name string) string {
	if fileRe.MatchString(name) {
		return esc(name)
	}
	return "«" + esc(name) + "»"
}

func (w *htmlWeaver) chunkLink(c *chunk) string {
	return fmt.Sprintf(`<a class="pg" href="#%s">%s §%d</a>`, c.id, label(c.name), c.num)
}

func (w *htmlWeaver) meta(c *chunk) string {
	d := w.d
	var bits []string
	if users := d.users(c); len(users) > 0 {
		var l []string
		for _, u := range users {
			l = append(l, w.chunkLink(u))
		}
		bits = append(bits, "used in "+strings.Join(l, ", "))
	}
	if !fileRe.MatchString(c.name) && len(d.partof[c.name]) > 0 {
		var l []string
		for _, f := range d.partof[c.name] {
			l = append(l, fmt.Sprintf(`<a href="#%s">%s</a>`, d.defs[f][0].id, esc(f)))
		}
		bits = append(bits, "part of "+strings.Join(l, ", "))
	}
	if len(bits) == 0 {
		return ""
	}
	return `<div class="meta">` + strings.Join(bits, " · ") + "</div>"
}

func (w *htmlWeaver) chunk(c *chunk, depth int) string {
	var title string
	if fileRe.MatchString(c.name) {
		title = `<span class="badge">file</span> <span class="fname">` + esc(c.name) + "</span>"
	} else {
		title = `<span class="cname">` + "«" + esc(c.name) + "»</span> <span class=\"eq\">≡</span>"
	}
	nested := ""
	if depth > 0 {
		nested = " nested"
	}
	h := []string{fmt.Sprintf(`<section class="chunk%s" id="%s">`, nested, c.id),
		fmt.Sprintf(`<div class="chunk-head"><a class="num" href="#%s">§%d</a> %s</div>`, c.id, c.num, title), w.meta(c)}
	for _, it := range c.items {
		switch it.kind {
		case "prose":
			h = append(h, w.prose(it.kids))
		case "code":
			h = append(h, w.code(it.lines))
		case "chunk":
			h = append(h, w.chunk(it.chunk, depth+1))
		case "head":
			h = append(h, w.heading(it))
		case "img":
			h = append(h, w.figure(it))
		case "hint":
			h = append(h, w.hint(it))
		}
	}
	h = append(h, "</section>")
	var out []string
	for _, x := range h {
		if x != "" {
			out = append(out, x)
		}
	}
	return strings.Join(out, "\n")
}

func (w *htmlWeaver) hint(it *item) string {
	return `<p class="hint"><b>to be filled in by an agent:</b> ` + esc(it.text) + "</p>"
}

func (w *htmlWeaver) heading(it *item) string {
	return fmt.Sprintf(`<h%d id="%s">%s</h%d>`, it.level, w.d.headID[it], w.inline(it.kids), it.level)
}

var (
	svgProlog = regexp.MustCompile(`(?s)<\?xml.*?\?>|<!DOCTYPE.*?>`)
)

func mimeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	}
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func (w *htmlWeaver) figure(it *item) string {
	path, cap := imgParts(it.bag)
	f := w.d.image(path)
	if f == nil {
		return `<p class="bad">missing figure ` + esc(path) + "</p>"
	}
	alt := cap
	if alt == "" {
		alt = path
	}
	var body string
	if strings.ToLower(filepath.Ext(path)) == ".svg" {
		svg := f.val
		if f.kind == "file" {
			b, _ := os.ReadFile(f.val)
			svg = string(b)
		}
		svg = strings.TrimSpace(svgProlog.ReplaceAllString(svg, ""))
		body = strings.Replace(svg, "<svg", `<svg role="img" aria-label="`+escAttr.Replace(alt)+`"`, 1)
	} else {
		data := []byte(f.val)
		if f.kind == "file" {
			data, _ = os.ReadFile(f.val)
		}
		body = fmt.Sprintf(`<img alt="%s" src="data:%s;base64,%s">`, escAttr.Replace(alt), mimeOf(path), base64.StdEncoding.EncodeToString(data))
	}
	s := `<figure class="fig">` + body
	if cap != "" {
		s += "<figcaption>" + esc(cap) + "</figcaption>"
	}
	return s + "</figure>"
}

func (w *htmlWeaver) bibliography() string {
	d := w.d
	var rows []string
	for _, key := range d.citeOrder {
		src, ok := d.bibsrc[key]
		if !ok {
			continue
		}
		p := bibParts(parseBibtex(src))
		var parts []string
		if p.authors != "" {
			parts = append(parts, stop(esc(p.authors)))
		} else {
			parts = append(parts, "")
		}
		parts = append(parts, "“"+stop(esc(p.title))+"”")
		var tail []string
		if strings.TrimSpace(p.venue) != "" {
			tail = append(tail, "<i>"+esc(p.venue)+"</i>")
		}
		for _, x := range []string{esc(p.vol), esc(p.pages), esc(p.year)} {
			if x != "" {
				tail = append(tail, x)
			}
		}
		if len(tail) > 0 {
			parts = append(parts, strings.Join(tail, ", ")+".")
		} else {
			parts = append(parts, "")
		}
		if p.url != "" {
			parts = append(parts, fmt.Sprintf(`<a href="%s">%s</a>`, p.url, esc(p.url)))
		} else if p.hp != "" {
			parts = append(parts, stop(esc(p.hp)))
		}
		if p.arxiv != "" {
			parts = append(parts, fmt.Sprintf(`<a href="https://arxiv.org/abs/%s">arXiv:%s</a>`, p.arxiv, p.arxiv))
		}
		var back []string
		for _, r := range d.citedIn[key] {
			if strings.HasPrefix(r.id, "c-") {
				back = append(back, fmt.Sprintf(`<a class="pg" href="#%s">%s</a>`, r.id, label(r.name)))
			} else {
				back = append(back, fmt.Sprintf(`<a href="#%s">%s</a>`, r.id, esc(r.name)))
			}
		}
		var nonEmpty []string
		for _, x := range parts {
			if x != "" {
				nonEmpty = append(nonEmpty, x)
			}
		}
		rows = append(rows, fmt.Sprintf(`<li id="bib-%s" value="%d">%s<div class="meta">cited in %s</div></li>`,
			key, d.citeNo[key], strings.Join(nonEmpty, " "), strings.Join(back, ", ")))
	}
	return `<section class="backmatter" id="bibliography"><h2>Bibliography</h2>` + "\n" + `<ol class="bib">` + strings.Join(rows, "\n") + "</ol></section>"
}

func sortedNames(d *Doc) []string {
	names := append([]string(nil), d.defOrder...)
	sort.SliceStable(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}

func (w *htmlWeaver) index() string {
	d := w.d
	var rows []string
	for _, name := range sortedNames(d) {
		c := d.defs[name][0]
		var us []string
		for _, u := range d.users(c) {
			us = append(us, fmt.Sprintf(`<a class="pg" href="#%s">§%d</a>`, u.id, u.num))
		}
		rows = append(rows, fmt.Sprintf(`<tr><td><a class="pg" href="#%s">%s</a></td><td class="n">§%d</td><td>%s</td></tr>`,
			c.id, label(name), c.num, strings.Join(us, ", ")))
	}
	return `<section class="backmatter" id="index"><h2>Chunk index</h2>` + "\n" +
		`<table class="index"><thead><tr><th>chunk</th><th>defined</th><th>used in</th></tr></thead><tbody>` +
		strings.Join(rows, "\n") + "</tbody></table></section>"
}

func (w *htmlWeaver) render() string {
	d := w.d
	var body []string
	firstH1 := true
	for _, it := range d.root.items {
		switch it.kind {
		case "head":
			if it.level == 1 && firstH1 {
				firstH1 = false
				body = append(body, fmt.Sprintf(`<h1 class="title" id="%s">%s</h1><p class="sub">Woven from <code>%s</code> — code chunks are numbered and cross-linked</p>@@CONTENTS@@`,
					d.headID[it], w.inline(it.kids), esc(d.srcName)))
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
	body = append(body, w.index(), w.bibliography())
	var toc []heading
	for _, h := range d.heads {
		if !(h.level == 1 && h.title == d.title) {
			toc = append(toc, h)
		}
	}
	toc = append(toc, heading{2, "Chunk index", "index"}, heading{2, "Bibliography", "bibliography"})
	var tocHTML strings.Builder
	for _, h := range toc {
		fmt.Fprintf(&tocHTML, `<li class="l%d"><a class="pg" href="#%s">%s</a></li>`, h.level, h.id, esc(h.title))
	}
	var files strings.Builder
	for _, n := range d.defOrder {
		if fileRe.MatchString(n) {
			fmt.Fprintf(&files, `<li><a class="pg" href="#%s">%s</a></li>`, d.defs[n][0].id, esc(n))
		}
	}
	contents := fmt.Sprintf(`<nav id="contents"><h2>Contents</h2><ul>%s</ul><h3>Files</h3><ul class="files">%s</ul></nav>`, tocHTML.String(), files.String())
	side := fmt.Sprintf(`<nav id="side"><div class="st">%s</div><ul>%s</ul><div class="st">Files</div><ul class="files">%s</ul></nav>`,
		esc(d.title), tocHTML.String(), files.String())
	css := weaveCSS
	mj := `<script>window.MathJax={tex:{inlineMath:[["\\(","\\)"]],displayMath:[["\\[","\\]"]]},` +
		`options:{skipHtmlTags:["script","noscript","style","textarea","pre","code"]}};</script>` +
		fmt.Sprintf(`<script async src="%s"></script>`, escAttr.Replace(w.mathjax))
	page := fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title>%s<style>%s</style></head><body>%s<main>`,
		esc(d.title), mj, css, side)
	return page + strings.Replace(strings.Join(body, "\n"), "@@CONTENTS@@", contents, -1) + "</main></body></html>"
}
