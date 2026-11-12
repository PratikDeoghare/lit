package lit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Inline pieces are swapped for private-use placeholders while prose is split into paragraphs and lists,
// then substituted back as HTML or LaTeX.
var (
	citeRun = regexp.MustCompile("\ue002[^\ue003]*\ue003(?:\\s+\ue002[^\ue003]*\ue003)*")
	citeOne = regexp.MustCompile("\ue002([^\ue003]*)\ue003")
	codePH  = regexp.MustCompile("\ue000(\\d+)\ue001")
	quotes  = regexp.MustCompile(`"([^"]*)"`)
	mathRe  = regexp.MustCompile(`(?s)^\s*\$(.+)\$\s*$`)
)

// ---- the document tree

type chunk struct {
	name   string
	parent *chunk
	items  []*item
	num    int
	id     string
	refs   []string // chunk names referenced from this chunk's code
}

// item kinds: prose (kids), code (lines), head (level, kids), chunk, bib (bag), img (bag), hint (text)
type item struct {
	kind  string
	kids  []Node
	lines []string
	level int
	chunk *chunk
	bag   Bag
	text  string
}

func plainKids(kids []Node) string {
	var sb strings.Builder
	for _, k := range kids {
		switch k := k.(type) {
		case Blob:
			sb.WriteString(k.Text())
		case Code:
			sb.WriteString(k.Text())
		}
	}
	return strings.TrimSpace(sb.String())
}

// runText turns inline kids into text with placeholders plus the table of code spans.
func runText(kids []Node) (string, []Code) {
	var sb strings.Builder
	var table []Code
	for _, k := range kids {
		switch k := k.(type) {
		case Blob:
			sb.WriteString(k.Text())
		case Code:
			table = append(table, k)
			fmt.Fprintf(&sb, "\ue000%d\ue001", len(table)-1)
		case Bag:
			sb.WriteString("\ue002" + plainKids(k.Kids()) + "\ue003")
		}
	}
	return sb.String(), table
}

func mathOf(c Code) (string, bool) {
	if m := mathRe.FindStringSubmatch(c.Text()); m != nil {
		return strings.TrimSpace(m[1]), true
	}
	return "", false
}

func buildChunk(b Bag, parent *chunk) *chunk {
	c := &chunk{parent: parent}
	if parent != nil {
		c.name = strings.TrimSpace(b.Tag())
	}
	var run []Node
	flush := func() {
		if len(run) > 0 {
			c.items = append(c.items, &item{kind: "prose", kids: append([]Node(nil), run...)})
			run = run[:0]
		}
	}
	for _, k := range b.Kids() {
		switch k := k.(type) {
		case Code:
			if isFence(k) {
				flush()
				c.items = append(c.items, &item{kind: "code", lines: FenceText(k)})
			} else {
				run = append(run, k)
			}
		case Bag:
			tag := strings.TrimSpace(k.Tag())
			switch {
			case tag == "cite":
				run = append(run, k)
			case tag == "img":
				flush()
				c.items = append(c.items, &item{kind: "img", bag: k})
			case isHeading(tag):
				flush()
				c.items = append(c.items, &item{kind: "head", level: int(tag[1] - '0'), kids: k.Kids()})
			case tag == "bibliography":
				flush()
				c.items = append(c.items, &item{kind: "bib", bag: k})
			case tag == HintTag:
				flush()
				c.items = append(c.items, &item{kind: "hint", text: PlainText(k)})
			default:
				flush()
				c.items = append(c.items, &item{kind: "chunk", chunk: buildChunk(k, c)})
			}
		default:
			run = append(run, k)
		}
	}
	flush()
	return c
}

// imgParts reads #img{`path` caption} (or #img{path caption}).
func imgParts(b Bag) (path, caption string) {
	var code *Code
	var blobs strings.Builder
	for _, k := range b.Kids() {
		switch k := k.(type) {
		case Code:
			if code == nil {
				kk := k
				code = &kk
			}
		case Blob:
			blobs.WriteString(k.Text())
		}
	}
	rest := strings.Join(strings.Fields(blobs.String()), " ")
	if code != nil {
		return strings.TrimSpace(code.Text()), rest
	}
	p, c, _ := strings.Cut(rest, " ")
	return p, strings.TrimSpace(c)
}

type pair struct {
	c  *chunk
	it *item
}

// walk lists every item in document order with the chunk that holds it.
func walk(c *chunk) []pair {
	var out []pair
	for _, it := range c.items {
		out = append(out, pair{c, it})
		if it.kind == "chunk" {
			out = append(out, walk(it.chunk)...)
		}
	}
	return out
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(nonAlnum.ReplaceAllString(s, "-"), "-")
	if s == "" {
		return "x"
	}
	return s
}

// ---- analysis shared by both backends

type heading struct {
	level     int
	title, id string
}

type placeRef struct{ name, id string }

type Doc struct {
	srcName, base string
	bag           Bag
	root          *chunk
	defs          map[string][]*chunk
	defOrder      []string
	uses          map[string][]*chunk
	useOrder      []string
	partof        map[string][]string
	bibsrc        map[string]string
	bibOrder      []string
	citeNo        map[string]int
	citeOrder     []string
	citedIn       map[string][]placeRef
	heads         []heading
	usedIDs       map[string]bool
	headID        map[*item]string
	headTitle     map[string]string
	title         string
	files         map[string]string
	filesDone     bool
	warn          io.Writer
}

func NewDoc(root Bag, srcName, base string, warn io.Writer) *Doc {
	if warn == nil {
		warn = io.Discard
	}
	if base == "" {
		base = "."
	}
	d := &Doc{srcName: srcName, base: base, bag: root, root: buildChunk(root, nil), warn: warn,
		defs: map[string][]*chunk{}, uses: map[string][]*chunk{}, partof: map[string][]string{}, bibsrc: map[string]string{},
		citeNo: map[string]int{}, citedIn: map[string][]placeRef{}, usedIDs: map[string]bool{}, headID: map[*item]string{},
		headTitle: map[string]string{}}
	d.analyse()
	d.title = strings.TrimSuffix(srcName, filepath.Ext(srcName))
	for _, h := range d.heads {
		if h.level == 1 {
			d.title = h.title
			break
		}
	}
	return d
}

type figure struct {
	kind string // "text" or "file"
	val  string
}

// image finds a figure: a file chunk of the program, or a file next to it.
func (d *Doc) image(path string) *figure {
	if !d.filesDone {
		d.filesDone = true
		d.files = map[string]string{}
		ch := Collect(d.bag, false)
		for _, n := range ch.Order {
			if fileRe.MatchString(n) {
				if lines, err := ch.Expand(n); err == nil {
					d.files[n] = strings.Join(lines, "\n") + "\n"
				}
			}
		}
	}
	if t, ok := d.files[path]; ok {
		return &figure{"text", t}
	}
	full := filepath.Join(d.base, path)
	if fi, err := os.Stat(full); err == nil && fi.Mode().IsRegular() {
		return &figure{"file", full}
	}
	return nil
}

func (d *Doc) uniqueID(base string) string {
	cand := base
	for i := 1; d.usedIDs[cand]; {
		i++
		cand = fmt.Sprintf("%s-%d", base, i)
	}
	d.usedIDs[cand] = true
	return cand
}

func citeKeys(text string) []string {
	var out []string
	for _, m := range citeOne.FindAllStringSubmatch(text, -1) {
		for _, k := range strings.Split(m[1], ",") {
			if k = strings.TrimSpace(k); k != "" {
				out = append(out, k)
			}
		}
	}
	return out
}

func (d *Doc) addDef(c *chunk) {
	if _, ok := d.defs[c.name]; !ok {
		d.defOrder = append(d.defOrder, c.name)
	}
	d.defs[c.name] = append(d.defs[c.name], c)
}

func (d *Doc) analyse() {
	n := 0
	items := walk(d.root)
	for _, p := range items {
		it := p.it
		switch it.kind {
		case "head":
			text, _ := runText(it.kids)
			title := strings.TrimSpace(text)
			hid := d.uniqueID("h-" + slug(title))
			d.heads = append(d.heads, heading{it.level, title, hid})
			d.headID[it], d.headTitle[hid] = hid, title
		case "chunk":
			n++
			c := it.chunk
			c.num, c.id = n, d.uniqueID("c-"+slug(c.name))
			d.addDef(c)
		case "bib":
			for _, k := range it.bag.Kids() {
				if kb, ok := k.(Bag); ok {
					var lines []string
					for _, kk := range kb.Kids() {
						if kc, ok := kk.(Code); ok && isFence(kc) {
							lines = append(lines, FenceText(kc)...)
						}
					}
					key := strings.TrimSpace(kb.Tag())
					if _, seen := d.bibsrc[key]; !seen {
						d.bibOrder = append(d.bibOrder, key)
					}
					d.bibsrc[key] = strings.Join(lines, "\n")
				}
			}
		}
	}
	var curHead *placeRef
	for _, p := range items {
		it, ch := p.it, p.c
		switch it.kind {
		case "head":
			hid := d.headID[it]
			curHead = &placeRef{d.headTitle[hid], hid}
		case "code":
			for _, ln := range it.lines {
				if m := refRe.FindStringSubmatch(ln); m != nil {
					name := strings.TrimSpace(m[2])
					ch.refs = append(ch.refs, name)
					if _, ok := d.uses[name]; !ok {
						d.useOrder = append(d.useOrder, name)
					}
					found := false
					for _, u := range d.uses[name] {
						found = found || u == ch
					}
					if !found {
						d.uses[name] = append(d.uses[name], ch)
					}
				}
			}
		case "prose":
			text, _ := runText(it.kids)
			for _, key := range citeKeys(text) {
				if _, ok := d.citeNo[key]; !ok {
					d.citeNo[key] = len(d.citeNo) + 1
					d.citeOrder = append(d.citeOrder, key)
				}
				var where *placeRef
				switch {
				case ch.name != "":
					where = &placeRef{ch.name, ch.id}
				case curHead != nil:
					nm := curHead.name
					if curHead.id == d.heads[0].id {
						nm = "introduction"
					}
					where = &placeRef{nm, curHead.id}
				}
				if where != nil {
					dup := false
					for _, w := range d.citedIn[key] {
						dup = dup || w == *where
					}
					if !dup {
						d.citedIn[key] = append(d.citedIn[key], *where)
					}
				}
			}
		}
	}
	for _, p := range items {
		if p.it.kind == "img" {
			if path, _ := imgParts(p.it.bag); d.image(path) == nil {
				fmt.Fprintf(d.warn, "warning: #img{%s}: no such file chunk or file\n", path)
			}
		}
	}
	for _, name := range d.useOrder {
		if _, ok := d.defs[name]; !ok {
			fmt.Fprintf(d.warn, "warning: <<<%s>>> is referenced but never defined\n", name)
		}
	}
	for _, f := range d.defOrder {
		if !fileRe.MatchString(f) {
			continue
		}
		seen := map[string]bool{f: true}
		queue := []string{f}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, c := range d.defs[cur] {
				for _, r := range c.refs {
					if _, ok := d.defs[r]; ok && !seen[r] {
						seen[r] = true
						queue = append(queue, r)
						d.partof[r] = append(d.partof[r], f)
					}
				}
			}
		}
	}
	for _, key := range d.citeOrder {
		if _, ok := d.bibsrc[key]; !ok {
			fmt.Fprintf(d.warn, "warning: #cite{%s} has no bibliography entry\n", key)
		}
	}
}

type block struct {
	kind  string // "p" | "ul" | "math"
	text  string
	items []string
}

// blocks splits a prose run into paragraphs, bullet lists and display math; text still has placeholders.
func (d *Doc) blocks(kids []Node) ([]block, []Code) {
	text, table := runText(kids)
	var paras [][]string
	var cur []string
	for _, ln := range append(strings.Split(text, "\n"), "") {
		if s := strings.TrimSpace(ln); s != "" {
			cur = append(cur, s)
		} else if len(cur) > 0 {
			paras = append(paras, cur)
			cur = nil
		}
	}
	var out []block
	for _, p := range paras {
		if strings.HasPrefix(p[0], "- ") {
			var items []string
			for _, ln := range p {
				if strings.HasPrefix(ln, "- ") {
					items = append(items, ln[2:])
				} else {
					items[len(items)-1] += " " + ln
				}
			}
			out = append(out, block{kind: "ul", items: items})
			continue
		}
		t := strings.Join(p, " ")
		if m := codePH.FindStringSubmatch(t); m != nil && m[0] == t {
			var i int
			fmt.Sscan(m[1], &i)
			if tex, ok := mathOf(table[i]); ok {
				out = append(out, block{kind: "math", text: tex})
				continue
			}
		}
		out = append(out, block{kind: "p", text: t})
	}
	return out, table
}

func (d *Doc) users(c *chunk) []*chunk {
	var out []*chunk
	for _, u := range d.uses[c.name] {
		if u != c {
			out = append(out, u)
		}
	}
	return out
}
