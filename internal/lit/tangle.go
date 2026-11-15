package lit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pratikdeoghare/brashtag-literate/internal/brashtag"
)

type (
	Bag  = brashtag.Bag
	Blob = brashtag.Blob
	Code = brashtag.Code
	Node = brashtag.Node
)

// Reserved tags: they are never chunks. Everything else that is a bag defines a chunk.
var Reserved = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"cite": true, "bibliography": true, "include": true, "img": true, "agent-fill-in": true,
}

const HintTag = "agent-fill-in"

var (
	refRe     = regexp.MustCompile(`^(\s*)<<<(.+?)>>>\s*$`)
	escapedRe = regexp.MustCompile(`^(\s*)\\(\\*<<<.+?>>>\s*)$`)
	fileRe    = regexp.MustCompile(`\.\w+$`)
)

func isHeading(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}

// ---- walking the tree with positions

// Walk visits every node with its byte offsets [start,end) in the text the tree was parsed from (the text
// inside the implicit root). The tree is lossless, so offsets come from adding up lengths. f returns false
// to skip the children of a bag. parents ends with the bag that holds the node.
func Walk(root Bag, f func(n Node, parents []Bag, start, end int) bool) {
	pos := 0
	var rec func(b Bag, parents []Bag)
	rec = func(b Bag, parents []Bag) {
		ps := append(append([]Bag{}, parents...), b)
		for _, k := range b.Kids() {
			switch k := k.(type) {
			case Blob:
				start := pos
				pos += len(k.Text())
				f(k, ps, start, pos)
			case Code:
				start := pos
				pos += 2*len(k.Tag()) + len(k.Text())
				f(k, ps, start, pos)
			case Bag:
				start := pos
				inner := innerLen(k)
				pos = start + len(k.Tag()) + 2
				if f(k, ps, start, start+len(k.Tag())+2+inner+1) {
					rec(k, ps)
				} else {
					pos += inner
				}
				pos++ // the closing brace
			}
		}
	}
	rec(root, nil)
}

func innerLen(b Bag) int {
	n := 0
	for _, k := range b.Kids() {
		switch k := k.(type) {
		case Blob:
			n += len(k.Text())
		case Code:
			n += 2*len(k.Tag()) + len(k.Text())
		case Bag:
			n += len(k.Tag()) + 2 + innerLen(k) + 1
		}
	}
	return n
}

// ---- parsing

// ParseText parses literate text into a root bag. where(offset) names a position in the text for messages.
func ParseText(text string, where func(int) string) (Bag, error) {
	const head = "#document{"
	root, rest, err := brashtag.ParseRest(head + text + "}")
	at := func(r string) int { return max(0, len(text)+1-len(r)) }
	if err != nil {
		off := at(rest)
		msg := err.Error()
		if strings.HasPrefix(msg, "unexpected {") {
			if i := strings.IndexByte(rest, '{'); i >= 0 {
				off = at(rest) + i
			}
			msg = "a { in prose. Put it inside backticks (a literal # { } or tick outside code is not allowed)"
		} else if rest == "" {
			off = len(text)
		}
		return Bag{}, fmt.Errorf("%s: %s", where(min(off, len(text))), msg)
	}
	if strings.TrimSpace(rest) != "" {
		off := at(rest) - 1
		snip := text[min(off, len(text)):]
		if len(snip) > 50 {
			snip = snip[:50]
		}
		return Bag{}, fmt.Errorf("%s: a stray '}' in prose closed the document early, near: %q  (a literal '}' or '#' outside backticks is not allowed)",
			where(min(off, len(text))), strings.TrimSpace(snip))
	}
	var bad error
	var check func(b Bag)
	check = func(b Bag) {
		t := b.Tag()
		if strings.Contains(t, "\n") || strings.Contains(t, "#") || t != strings.TrimLeft(t, " \t\r\n") || len(t) > 120 {
			i := strings.Index(text, "#"+t[:min(30, len(t))])
			if bad == nil {
				bad = fmt.Errorf("%s: suspicious bag tag %q...: a stray '#' in prose? (use backticks)", where(max(i, 0)), t[:min(40, len(t))])
			}
		}
		for _, k := range b.Kids() {
			if kb, ok := k.(Bag); ok {
				check(kb)
			}
		}
	}
	check(root)
	if bad != nil {
		return Bag{}, bad
	}
	return root, nil
}

// ParseFlat parses program text that is already spliced together (what Source.Text holds).
func ParseFlat(text string) (Bag, error) {
	return ParseText(text, func(off int) string {
		return fmt.Sprintf("program text line %d", strings.Count(text[:min(off, len(text))], "\n")+1)
	})
}

// Load parses a .lit file and the chapter files it includes.
func Load(path string) (Bag, error) {
	s, err := ReadSource(path)
	if err != nil {
		return Bag{}, err
	}
	return s.Parse()
}

// ---- chunks

func isFence(c Code) bool { return len(c.Tag()) >= 3 }

// FenceText gives the lines of a fenced block: the newline after the opening fence is dropped, and the lines
// are dedented by the whitespace that precedes the closing fence.
func FenceText(c Code) []string {
	lines := strings.Split(c.Text(), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	indent := ""
	if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) == "" {
		indent, lines = lines[n-1], lines[:n-1]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if strings.HasPrefix(l, indent) {
			out[i] = l[len(indent):]
		} else {
			out[i] = strings.TrimLeft(l, " \t")
		}
	}
	return out
}

// Chunks holds the code lines of every chunk (all fenced blocks of every definition, in document order).
type Chunks struct {
	Order    []string
	Lines    map[string][]string
	Warnings []string
	Holes    []Hole
}

// A Hole is an #agent-fill-in{} hint found inside a chunk. When holes are asked for, Collect leaves a marker
// line (see HoleMarker) where the hint was, so that expansion carries its indentation.
type Hole struct {
	Index int
	Chunk string
	Bag   Bag
	Text  string // the hint, as plain text
}

func HoleMarker(i int) string { return fmt.Sprintf("\x00hole:%d", i) }

var holeRe = regexp.MustCompile("^\\s*\x00hole:(\\d+)$")

// Collect gathers the chunks of a document. With holes true, hints are recorded and marked in the code.
func Collect(root Bag, holes bool) *Chunks {
	c := &Chunks{Lines: map[string][]string{}}
	var visit func(b Bag)
	visit = func(b Bag) {
		name := strings.TrimSpace(b.Tag())
		if Reserved[name] {
			return
		}
		if _, ok := c.Lines[name]; !ok {
			c.Order = append(c.Order, name)
			c.Lines[name] = nil
		}
		for _, k := range b.Kids() {
			switch k := k.(type) {
			case Code:
				if isFence(k) {
					c.Lines[name] = append(c.Lines[name], FenceText(k)...)
				}
			case Bag:
				if k.Tag() == HintTag {
					if holes {
						h := Hole{Index: len(c.Holes), Chunk: name, Bag: k, Text: PlainText(k)}
						c.Holes = append(c.Holes, h)
						c.Lines[name] = append(c.Lines[name], HoleMarker(h.Index))
					}
					continue
				}
				visit(k)
			}
		}
	}
	for _, k := range root.Kids() {
		switch k := k.(type) {
		case Code:
			if isFence(k) {
				c.Warnings = append(c.Warnings, "code outside any chunk is ignored")
			}
		case Bag:
			visit(k)
		}
	}
	return c
}

// PlainText is the text of a bag's blobs and code, with runs of white space made single spaces.
func PlainText(b Bag) string {
	var sb strings.Builder
	for _, k := range b.Kids() {
		switch k := k.(type) {
		case Blob:
			sb.WriteString(k.Text())
		case Code:
			sb.WriteString(k.Text())
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

// Unescape is the text tangle writes for a code line: an escaped reference line loses its first backslash.
func Unescape(line string) string {
	if m := escapedRe.FindStringSubmatch(line); m != nil {
		return m[1] + m[2]
	}
	return line
}

// Expand returns the lines of chunk name with every reference replaced by the lines of its chunk, indented
// by the reference's own indentation.
func (c *Chunks) Expand(name string) ([]string, error) { return c.expand(name, nil) }

func (c *Chunks) expand(name string, trail []string) ([]string, error) {
	lines, ok := c.Lines[name]
	if !ok {
		return nil, fmt.Errorf("undefined chunk <<<%s>>>", name)
	}
	for _, t := range trail {
		if t == name {
			return nil, fmt.Errorf("recursive chunk: %s", strings.Join(append(append([]string{}, trail...), name), " -> "))
		}
	}
	var out []string
	for _, line := range lines {
		if m := refRe.FindStringSubmatch(line); m != nil {
			sub, err := c.expand(strings.TrimSpace(m[2]), append(append([]string{}, trail...), name))
			if err != nil {
				return nil, err
			}
			for _, s := range sub {
				if s != "" {
					s = m[1] + s
				}
				out = append(out, s)
			}
		} else if holeRe.MatchString(line) {
			out = append(out, line)
		} else {
			out = append(out, Unescape(line))
		}
	}
	return out, nil
}

// FileNames are the chunks that are written out as files, in the order they were first defined.
func (c *Chunks) FileNames() []string {
	var out []string
	for _, n := range c.Order {
		if fileRe.MatchString(n) {
			out = append(out, n)
		}
	}
	return out
}

// FileBody is what tangle writes for a file made of these lines.
func FileBody(lines []string) string {
	body := strings.Join(lines, "\n")
	if body == "" {
		return ""
	}
	return body + "\n"
}

// Used returns the names of the chunks that some code line references.
func (c *Chunks) Used() map[string]bool {
	used := map[string]bool{}
	for _, lines := range c.Lines {
		for _, l := range lines {
			if m := refRe.FindStringSubmatch(l); m != nil {
				used[strings.TrimSpace(m[2])] = true
			}
		}
	}
	return used
}

// TangleText tangles program text given as a string: {file: generated text}, in definition order.
func TangleText(text string) (map[string]string, []string, error) {
	root, err := ParseFlat(text)
	if err != nil {
		return nil, nil, err
	}
	c := Collect(root, false)
	out := map[string]string{}
	var order []string
	for _, n := range c.FileNames() {
		lines, err := c.Expand(n)
		if err != nil {
			return nil, nil, err
		}
		out[n] = FileBody(lines)
		order = append(order, n)
	}
	return out, order, nil
}

// ---- the command

const tangleUsage = "usage: lit tangle SOURCE.lit [OUTDIR] [--no-map]"

func Tangle(argv []string, io IO) int {
	a, err := ParseArgs(argv, Spec{Opts: map[string]bool{"no-map": false}})
	if err != nil || len(a.Pos) == 0 || len(a.Pos) > 2 {
		fmt.Fprintln(io.Err, tangleUsage)
		return 2
	}
	srcPath, outdir := a.Pos[0], "."
	if len(a.Pos) > 1 {
		outdir = a.Pos[1]
	}
	src, err := ReadSource(srcPath)
	if err != nil {
		fmt.Fprintln(io.Err, err)
		return 1
	}
	root, err := src.Parse()
	if err != nil {
		fmt.Fprintln(io.Err, err)
		return 1
	}
	chunks := Collect(root, true)
	for _, w := range chunks.Warnings {
		fmt.Fprintln(io.Err, "warning: "+w)
	}
	used := chunks.Used()
	for _, n := range chunks.Order {
		if !fileRe.MatchString(n) && !used[n] && len(chunks.Lines[n]) > 0 {
			fmt.Fprintf(io.Err, "warning: chunk '%s' is never used\n", n)
		}
	}
	if len(chunks.Holes) > 0 {
		fmt.Fprintf(io.Err, "note: %d #%s hint(s) are still unfilled; the code they stand for is missing from the output (lit fill)\n", len(chunks.Holes), HintTag)
	}
	files := map[string]string{}
	for _, n := range chunks.FileNames() {
		lines, err := chunks.Expand(n)
		if err != nil {
			fmt.Fprintln(io.Err, err)
			return 1
		}
		files[n] = FileBody(withoutHoles(lines))
	}
	for _, n := range chunks.FileNames() {
		path := filepath.Join(outdir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(io.Err, err)
			return 1
		}
		protect(srcPath, outdir, n, path, files[n])
		if err := os.WriteFile(path, []byte(files[n]), 0o644); err != nil {
			fmt.Fprintln(io.Err, err)
			return 1
		}
		fmt.Fprintln(io.Out, "wrote", path)
	}
	if !a.Has("no-map") {
		p, err := WriteMap(srcPath, outdir)
		if err != nil {
			fmt.Fprintf(io.Err, "note: no sourcemap written: %v\n", err)
		} else {
			fmt.Fprintln(io.Out, "wrote", p)
		}
	}
	return 0
}

func withoutHoles(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !holeRe.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}
