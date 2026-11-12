// Command mkpage builds the landing page, one self-contained HTML file, from the pieces tangled into web/.
//
//	cd web && BRASHTAG_JS=path/to/parser.js LIT_STATS="$(lit lint ../book.lit | tail -n 1)" mkpage > landing.html
//
// It needs nothing but the standard library.
package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	escText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	escAttr = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")
)

func esc(s string) string { return escText.Replace(s) }

func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fatal("%v", err)
	}
	return string(b)
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "mkpage: "+f+"\n", a...)
	os.Exit(1)
}

// A run is one recorded command: what was typed, what it printed and how it ended.
type run struct {
	cmd  string
	out  []string
	code int
	has  bool
}

var (
	homeRe = regexp.MustCompile(`/[^\s'"]*/home\b`)
	exitRe = regexp.MustCompile(`^\[exit (\d+)\]$`)
)

// scrub: the recorded runs used a scratch home directory; show it as ~.
func scrub(s string) string { return homeRe.ReplaceAllString(s, "~") }

// transcripts reads t1.txt .. t9.txt: a line that starts with "$ " begins a command, the lines after it are its
// output, and a line "[exit N]" ends it. Lines that start with "# (" are comments for the reader of the file.
func transcripts() map[string]*run {
	T := map[string]*run{}
	for n := 1; n <= 9; n++ {
		var cur *run
		for _, line := range strings.Split(read(fmt.Sprintf("tut/t%d.txt", n)), "\n") {
			switch {
			case strings.HasPrefix(line, "$ "):
				cur = &run{cmd: line[2:]}
				T[fmt.Sprintf("t%d:%s", n, cur.cmd)] = cur
			case strings.HasPrefix(line, "# ("):
				cur = nil
			case cur != nil:
				if m := exitRe.FindStringSubmatch(line); m != nil {
					cur.code, _ = strconv.Atoi(m[1])
					cur.has = true
					cur = nil
				} else {
					cur.out = append(cur.out, line)
				}
			}
		}
	}
	return T
}

func trimTrailingBlank(l []string) []string {
	for len(l) > 0 && l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}

func termBlock(cmd string, out string, badge string) string {
	s := fmt.Sprintf(`<div class="term"><button class="btn copy" data-copy="%s">Copy</button><pre><span class="p">$ </span><span class="c">%s</span>`, escAttr.Replace(cmd), esc(cmd))
	if out != "" {
		s += fmt.Sprintf("\n"+`<span class="o">%s</span>`, esc(scrub(out)))
	}
	return s + "</pre>" + badge + "</div>"
}

func term(T map[string]*run, key, shown string) string {
	r, ok := T[key]
	if !ok {
		fatal("no transcript %s", key)
	}
	out := trimTrailingBlank(append([]string(nil), r.out...))
	cmd := r.cmd
	if shown != "" {
		cmd = shown
	}
	badge := ""
	if r.code != 0 && strings.HasPrefix(cmd, "lit ") {
		badge = fmt.Sprintf(`<span class="exit">exit %d</span>`, r.code)
	}
	return termBlock(cmd, strings.Join(out, "\n"), badge)
}

func codefile(text, name string) string {
	n := len(strings.Split(strings.TrimRight(text, "\n"), "\n"))
	if strings.TrimSpace(text) == "" {
		n = 0
	}
	return fmt.Sprintf(`<div class="codefile"><div class="file-head"><span>%s</span><span>%d lines</span></div><pre>%s</pre></div>`, esc(name), n, esc(strings.TrimRight(text, "\n")))
}

// pick rebuilds the interactive session of `lit sync pick`: the recording ran without a terminal, so the answers
// the tutorial gives are put after the prompts, and the closing line comes from the run that wrote the files.
func pick(T map[string]*run) string {
	r, ok := T["t6:lit sync invoice.lit pick"]
	r2, ok2 := T["t7:lit sync invoice.lit pick"]
	if !ok || !ok2 {
		fatal("no transcripts of lit sync pick")
	}
	answers := []string{"y", "n"}
	var res []string
	for _, l := range r.out {
		switch {
		case strings.HasPrefix(l, "apply to the") && strings.HasSuffix(l, "> "):
			l += answers[0]
			answers = answers[1:]
		case strings.HasPrefix(l, "Write these changes? [y/n] > nothing written."):
			l = "Write these changes? [y/n] > y"
		}
		res = append(res, l)
	}
	last := ""
	for _, l := range r2.out {
		if strings.HasPrefix(l, "synced.") {
			last = l
			break
		}
	}
	res = trimTrailingBlank(append(res, "", last))
	return termBlock("lit sync invoice.lit pick", strings.Trim(strings.Join(res, "\n"), "\n"), "")
}

var (
	placeholder = regexp.MustCompile(`\[\[(.+?)\]\]`)
	prologRe    = regexp.MustCompile(`(?s)<\?xml.*?\?>|<!DOCTYPE.*?>`)
)

func expand(T map[string]*run, t string) string {
	return placeholder.ReplaceAllStringFunc(t, func(m string) string {
		tok := m[2 : len(m)-2]
		if tok == "pick" {
			return pick(T)
		}
		kind, rest, _ := strings.Cut(tok, ":")
		switch kind {
		case "term":
			key, shown, _ := strings.Cut(rest, "|")
			return term(T, key, shown)
		case "svg":
			f, cap, ok := strings.Cut(rest, "|")
			if !ok {
				fatal("bad svg placeholder %s", tok)
			}
			s := strings.TrimSpace(prologRe.ReplaceAllString(read(f), ""))
			s = strings.Replace(s, "<svg", `<svg role="img" aria-label="`+html.EscapeString(cap)+`"`, 1)
			return fmt.Sprintf(`<figure class="diagram"><div class="scroll">%s</div><figcaption>%s</figcaption></figure>`, s, esc(cap))
		case "cmd":
			return termBlock(rest, "", "")
		case "code":
			p := strings.Split(rest, ":")
			if len(p) != 2 {
				fatal("bad code placeholder %s", tok)
			}
			return codefile(read("tut/"+p[0]), p[1])
		case "file":
			p := strings.Split(rest, ":")
			if len(p) != 4 {
				fatal("bad file placeholder %s", tok)
			}
			a, _ := strconv.Atoi(p[1])
			z, _ := strconv.Atoi(p[2])
			L := strings.Split(read("tut/"+p[0]), "\n")
			return codefile(strings.Join(L[a-1:z], "\n"), p[3]+" (excerpt)")
		}
		fatal("unknown placeholder %s", tok)
		return ""
	})
}

var statsRe = regexp.MustCompile(`(\d+) chunks, (\d+) code lines, (\d+) words of prose.*?(\d+) error\(s\), (\d+) warning\(s\)`)

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// inlineParser makes the JavaScript build of the brashtag parser usable as plain script: the export keyword goes,
// and the parser's error for a stray opening brace gets the rest of the text, so the page can say where it is.
func inlineParser(path string) string {
	s := strings.ReplaceAll(read(path), "export ", "")
	old := "error: \"unexpected {\"\n        };"
	if !strings.Contains(s, old) {
		fatal("%s: the parser's error for an unexpected { has changed; update inlineParser", path)
	}
	return strings.Replace(s, old, "error: \"unexpected {\",\n          rest: text.slice(j)\n        };", 1)
}

var presetNames = []struct{ id, name, file string }{
	{"merge", "Merge sort", "merge_sort.lit"}, {"twice", "Same name twice", "imports.lit"}, {"files", "One chunk, two files", "hello.lit"},
	{"escape", "Escape a reference", "notation.lit"}, {"math", "Math and a citation", "softmax.lit"}, {"oops", "Break it", "oops.lit"},
	{"lab", "lab", "tip.lit"},
}

func main() {
	css, body := read("page.css"), read("page.body.html")
	T := transcripts()
	body = strings.Replace(body, "[[TUTORIAL]]", read("tutorial.html"), 1)
	body = strings.Replace(body, "[[INSTALL]]", read("install.html"), 1)
	body = expand(T, body)
	// numbers about the program itself: the build passes the linter's summary line in LIT_STATS
	if m := statsRe.FindStringSubmatch(os.Getenv("LIT_STATS")); m != nil {
		n := make([]int, 5)
		for i := range n {
			n[i], _ = strconv.Atoi(m[i+1])
		}
		for _, kv := range [][2]string{{"__CHUNKS__", commas(n[0])}, {"__CODE__", commas(n[1])}, {"__PROSE__", commas(n[2])}, {"__LINT__", fmt.Sprintf("%d error(s), %d warning(s)", n[3], n[4])}} {
			body = strings.ReplaceAll(body, kv[0], kv[1])
		}
	} else {
		for _, k := range []string{"__CHUNKS__", "__CODE__", "__PROSE__", "__LINT__"} {
			body = strings.ReplaceAll(body, k, "…")
		}
	}
	var pre strings.Builder
	for _, p := range presetNames {
		t := read(filepath.Join("presets", p.id+".lit"))
		if strings.Contains(t, "</script") {
			fatal("preset %s contains </script", p.id)
		}
		fmt.Fprintf(&pre, "<script type=\"text/plain\" data-preset=\"%s\" data-id=\"%s\" data-file=\"%s\">\n%s</script>\n", p.name, p.id, p.file, t)
	}
	parser := os.Getenv("BRASHTAG_JS")
	if parser == "" {
		parser = "node_modules/brashtag/parser.js"
	}
	core := inlineParser(parser) + "\n" + read("core.js") + "\n" + read("ui.js")
	if len(os.Args) > 1 && os.Args[1] == "--parser-only" {
		fmt.Print(inlineParser(parser))
		return
	}
	fmt.Printf(`<title>Brashtag Literate</title>
<meta name="claude:viewport-fit-sides" content="cover">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;600&family=Literata:ital,opsz,wght@0,7..72,400;0,7..72,600;1,7..72,400&family=Young+Serif&display=swap">
<style>
%s
</style>
%s
%s
<script>
(function(){
"use strict";
%s
})();
</script>
`, css, body, pre.String(), core)
}
