package lit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var digits = regexp.MustCompile(`^\d+$`)

// ParseAddress reads FILE:LINE[:COL] or LINE[:COL]; file is "" for a bare line.
func ParseAddress(s string) (file string, line, col int, err error) {
	p := strings.Split(s, ":")
	num := func(x string) int { n, _ := strconv.Atoi(x); return n }
	n := len(p)
	switch {
	case n >= 3 && digits.MatchString(p[n-1]) && digits.MatchString(p[n-2]):
		return strings.Join(p[:n-2], ":"), num(p[n-2]), num(p[n-1]), nil
	case n == 2 && digits.MatchString(p[0]) && digits.MatchString(p[1]):
		return "", num(p[0]), num(p[1]), nil
	case n >= 2 && digits.MatchString(p[n-1]):
		return strings.Join(p[:n-1], ":"), num(p[n-1]), 0, nil
	case n == 1 && digits.MatchString(p[0]):
		return "", num(p[0]), 0, nil
	}
	return "", 0, 0, fmt.Errorf("cannot read address %q; expected FILE:LINE[:COL] or LINE[:COL]", s)
}

// alignment maps the line numbers two versions of a file share (1-based).
type alignment struct{ c2p, p2c map[int]int }

func align(pristine, current []string) *alignment {
	al := &alignment{map[int]int{}, map[int]int{}}
	i, j := 0, 0
	link := func(n int) {
		for k := 0; k < n; k++ {
			al.p2c[i+k+1], al.c2p[j+k+1] = j+k+1, i+k+1
		}
	}
	for _, h := range Hunks(pristine, current) {
		link(h.A0 - i)
		i, j = h.A1, h.B1
	}
	link(len(pristine) - i)
	return al
}

// ranges turns [3,4,5,9] into ["3-5", "9"].
func ranges(nums []int) []string {
	seen := map[int]bool{}
	var u []int
	for _, n := range nums {
		if !seen[n] {
			seen[n] = true
			u = append(u, n)
		}
	}
	sort.Ints(u)
	var out []string
	for i := 0; i < len(u); {
		j := i
		for j+1 < len(u) && u[j+1] == u[j]+1 {
			j++
		}
		if j > i {
			out = append(out, fmt.Sprintf("%d-%d", u[i], u[j]))
		} else {
			out = append(out, fmt.Sprint(u[i]))
		}
		i = j + 1
	}
	return out
}

// AddrResult is one answer: a place, a range of places for a reference, or a note that the line was edited.
type AddrResult struct {
	Status    string   `json:"status,omitempty"`
	Msg       string   `json:"msg,omitempty"`
	File      string   `json:"file,omitempty"`
	Line      int      `json:"line,omitempty"`
	Col       int      `json:"col,omitempty"`
	Lines     []string `json:"lines,omitempty"`
	Chunk     string   `json:"chunk,omitempty"`
	Note      string   `json:"note,omitempty"`
	Reference bool     `json:"reference,omitempty"`
}

// Mapper answers address questions with a sourcemap.
type Mapper struct {
	Source, Outdir string
	Src            *Source
	M              *Map
	Stale          bool
	Notes          []string
	org            map[string][]Origin
	align          map[string]*alignment
	pristine       map[string]string
}

func NewMapper(source, outdir, mapFile string, ignoreStale bool) (*Mapper, error) {
	src, err := ReadSource(source)
	if err != nil {
		return nil, err
	}
	if mapFile == "" {
		mapFile = MapPath(source, outdir)
	}
	m, err := LoadMap(mapFile)
	if err != nil {
		return nil, err
	}
	mp := &Mapper{Source: source, Outdir: outdir, Src: src, M: m, org: map[string][]Origin{}, align: map[string]*alignment{}}
	mp.Stale = sha256hex(src.Text) != m.SourceSha256
	if mp.Stale && !ignoreStale {
		return nil, fmt.Errorf("%s has changed since it was tangled, so the sourcemap is out of date; run lit tangle again (or pass --ignore-stale to look anyway)", source)
	}
	for f, e := range m.Files {
		mp.org[f] = e.Origins()
	}
	return mp, nil
}

func (mp *Mapper) genPath(f string) string {
	return filepath.Clean(filepath.Join(mp.Outdir, filepath.FromSlash(f)))
}

// SourceName is the name of the source file (the root or a chapter) an address means, or "".
func (mp *Mapper) SourceName(name string) string {
	cands := []string{name}
	if a, err := filepath.Abs(name); err == nil {
		if b, err := filepath.Abs(mp.Src.Base); err == nil {
			if r, err := filepath.Rel(b, a); err == nil {
				cands = append(cands, r)
			}
		}
	}
	for _, c := range cands {
		c = filepath.ToSlash(filepath.Clean(c))
		if _, ok := mp.Src.Texts[c]; ok {
			return c
		}
	}
	return ""
}

func (mp *Mapper) sourcePath(name string) string {
	return filepath.Clean(filepath.Join(mp.Src.Base, filepath.FromSlash(name)))
}

func (mp *Mapper) generatedName(name string) (string, error) {
	if _, ok := mp.M.Files[name]; ok {
		return name, nil
	}
	if r, err := filepath.Rel(mp.Outdir, name); err == nil {
		r = filepath.ToSlash(r)
		if _, ok := mp.M.Files[r]; ok {
			return r, nil
		}
	}
	return "", fmt.Errorf("%s is neither %s nor a file generated from it (%s)", name, mp.Source, strings.Join(mp.M.FileOrder, ", "))
}

// FlatLine maps line `line` of source file `name` to its line in the program text the map was built from.
func (mp *Mapper) FlatLine(name string, line int) (int, error) {
	total := strings.Count(mp.Src.Texts[name], "\n") + 1
	if line < 1 || line > total {
		return 0, fmt.Errorf("%s has %d lines, not %d", name, total, line)
	}
	off, ok := mp.Src.FlatOffset(name, line, 1)
	if !ok {
		return 0, fmt.Errorf("%s:%d is an #include line (or lies outside the program text); address a line of the chapter itself", name, line)
	}
	return strings.Count(mp.Src.Text[:off], "\n") + 1, nil
}

func (mp *Mapper) alignment(f string) *alignment {
	if al, ok := mp.align[f]; ok {
		return al
	}
	var res *alignment
	path := filepath.Join(mp.Outdir, filepath.FromSlash(f))
	if !mp.Stale {
		if cur, err := ReadDisk(path); err == nil && sha256hex(cur) != mp.M.Files[f].Sha256 {
			if mp.pristine == nil {
				mp.pristine, _, _ = TangleText(mp.Src.Text)
			}
			res = align(ToLines(mp.pristine[f]), ToLines(cur))
			mp.Notes = append(mp.Notes, fmt.Sprintf("%s was edited after tangling; its line numbers are those of the current file", mp.genPath(f)))
		}
	}
	mp.align[f] = res
	return res
}

// ToLit maps a place in a generated file to the line of the program that produced it.
func (mp *Mapper) ToLit(f string, line, col int) ([]AddrResult, error) {
	pl := line
	if al := mp.alignment(f); al != nil {
		var ok bool
		if pl, ok = al.c2p[line]; !ok {
			return []AddrResult{{Status: "edited", Msg: fmt.Sprintf("%s:%d was added or changed after tangling, so it has no origin in the .lit yet (absorb folds it in)", mp.genPath(f), line)}}, nil
		}
	}
	org := mp.org[f]
	if pl < 1 || pl > len(org) {
		return nil, fmt.Errorf("%s has %d lines, not %d", mp.genPath(f), len(org), line)
	}
	o := org[pl-1]
	blk := mp.M.Blocks[o.Block]
	start, end := blk.Lines[o.Line][0], blk.Lines[o.Line][1]
	indent := len(blk.Indent)
	note := ""
	off := start + indent
	if col > 0 {
		if col <= utf8.RuneCountInString(o.Prefix) {
			note = "column lies in indentation added by a <<<reference>>>"
		} else {
			off = start + indent + byteOffset(mp.Src.Text[start+indent:end], col-utf8.RuneCountInString(o.Prefix)-1)
		}
	}
	name, ln, c := mp.Src.Locate(min(off, end))
	return []AddrResult{{File: mp.sourcePath(name), Line: ln, Col: c, Chunk: blk.Chunk, Note: note}}, nil
}

// byteOffset is the byte position of the n-th character of s (capped at the end).
func byteOffset(s string, n int) int {
	i := 0
	for ; n > 0 && i < len(s); n-- {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return i
}

func (mp *Mapper) lineSpan(line int) (lo, hi int, err error) {
	text := mp.Src.Text
	total := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		total++
	}
	if line < 1 || line > max(total, 1) {
		return 0, 0, fmt.Errorf("%s has %d lines, not %d", mp.Source, total, line)
	}
	for i := 1; i < line; i++ {
		lo += strings.IndexByte(text[lo:], '\n') + 1
	}
	hi = len(text)
	if k := strings.IndexByte(text[lo:], '\n'); k >= 0 {
		hi = lo + k
	}
	return lo, hi, nil
}

type genPlace struct {
	file string
	line int
	pfx  string
}

func (mp *Mapper) genPlaces(want map[[2]int]bool) []genPlace {
	var hits []genPlace
	for _, f := range mp.M.FileOrder {
		for _, s := range mp.M.Files[f].Segments {
			for k := 0; k < s.N; k++ {
				if want[[2]int{s.Block, s.Line + k}] {
					hits = append(hits, genPlace{f, s.At + k, s.Prefix})
				}
			}
		}
	}
	return hits
}

func (mp *Mapper) currentLine(f string, pristine int) (int, bool) {
	al := mp.alignment(f)
	if al == nil {
		return pristine, true
	}
	n, ok := al.p2c[pristine]
	return n, ok
}

// ToGen maps a line of the program (a line of the flat text) to the generated places it ends up in.
func (mp *Mapper) ToGen(line, col int) ([]AddrResult, error) {
	lo, hi, err := mp.lineSpan(line)
	if err != nil {
		return nil, err
	}
	var here [][2]int
	for bid, blk := range mp.M.Blocks {
		for li, l := range blk.Lines {
			if lo <= l[0] && l[1] <= hi {
				here = append(here, [2]int{bid, li})
			}
		}
	}
	if len(here) == 0 {
		return nil, nil
	}
	want := map[[2]int]bool{}
	for _, h := range here {
		want[h] = true
	}
	if hits := mp.genPlaces(want); len(hits) > 0 {
		blk := mp.M.Blocks[here[0][0]]
		var res []AddrResult
		for _, h := range hits {
			cl, ok := mp.currentLine(h.file, h.line)
			if !ok {
				res = append(res, AddrResult{Status: "edited", Msg: fmt.Sprintf("%s: the line generated here was changed after tangling", mp.genPath(h.file))})
				continue
			}
			gcol := utf8.RuneCountInString(h.pfx) + 1
			if col > 0 {
				gcol = utf8.RuneCountInString(h.pfx) + max(1, col-utf8.RuneCountInString(blk.Indent))
			}
			res = append(res, AddrResult{File: mp.genPath(h.file), Line: cl, Col: gcol, Chunk: blk.Chunk})
		}
		return res, nil
	}
	if m := refRe.FindStringSubmatch(mp.Src.Text[lo:hi]); m != nil {
		return mp.referencePlaces(strings.TrimSpace(m[2])), nil
	}
	return nil, nil
}

// referencePlaces gives the generated lines of chunk name and of every chunk it expands.
func (mp *Mapper) referencePlaces(name string) []AddrResult {
	reach := map[string]bool{}
	todo := []string{name}
	for len(todo) > 0 {
		n := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if reach[n] {
			continue
		}
		reach[n] = true
		for _, blk := range mp.M.Blocks {
			if blk.Chunk != n {
				continue
			}
			for _, l := range blk.Lines {
				if r := refRe.FindStringSubmatch(mp.Src.Text[l[0]:l[1]]); r != nil {
					todo = append(todo, strings.TrimSpace(r[2]))
				}
			}
		}
	}
	want := map[[2]int]bool{}
	for bid, blk := range mp.M.Blocks {
		if reach[blk.Chunk] {
			for li := range blk.Lines {
				want[[2]int{bid, li}] = true
			}
		}
	}
	perFile := map[string][]int{}
	var order []string
	for _, h := range mp.genPlaces(want) {
		if cl, ok := mp.currentLine(h.file, h.line); ok {
			if _, seen := perFile[h.file]; !seen {
				order = append(order, h.file)
			}
			perFile[h.file] = append(perFile[h.file], cl)
		}
	}
	var out []AddrResult
	for _, f := range order {
		out = append(out, AddrResult{File: mp.genPath(f), Lines: ranges(perFile[f]), Chunk: name, Reference: true})
	}
	return out
}

func renderAddr(r AddrResult) string {
	switch {
	case r.Status != "":
		return "# " + r.Msg
	case r.Reference:
		return fmt.Sprintf("%s:%s  (where «%s» is expanded)", r.File, strings.Join(r.Lines, ","), r.Chunk)
	}
	s := fmt.Sprintf("%s:%d:%d  «%s»", r.File, r.Line, r.Col, r.Chunk)
	if r.Note != "" {
		s += "  (" + r.Note + ")"
	}
	return s
}

var addrSpec = Spec{Opts: map[string]bool{"outdir": true, "map": true, "json": false, "ignore-stale": false}, Short: map[string]string{"o": "outdir"}}

// Addr translates addresses between the program and its generated files.
func Addr(argv []string, io IO) int {
	a, err := ParseArgs(argv, addrSpec)
	if err != nil || len(a.Pos) < 2 {
		fmt.Fprintln(io.Err, "usage: lit addr SOURCE.lit ADDRESS [ADDRESS ...] [-o OUTDIR] [--json] [--map PATH] [--ignore-stale]")
		return 2
	}
	type entry struct {
		Address   string       `json:"address"`
		Direction string       `json:"direction"`
		Results   []AddrResult `json:"results"`
	}
	fail := func(err error) int { fmt.Fprintln(io.Err, "addr: "+err.Error()); return 2 }
	mp, err := NewMapper(a.Pos[0], a.Get("outdir", "."), a.Get("map", ""), a.Has("ignore-stale"))
	if err != nil {
		return fail(err)
	}
	var report []entry
	found := true
	for _, addr := range a.Pos[1:] {
		fname, line, col, err := ParseAddress(addr)
		if err != nil {
			return fail(err)
		}
		var res []AddrResult
		dir := "gen->lit"
		if fname == "" || mp.SourceName(fname) != "" {
			name := mp.Src.Rel(mp.Src.Root)
			if fname != "" {
				name = mp.SourceName(fname)
			}
			fl, err := mp.FlatLine(name, line)
			if err != nil {
				return fail(err)
			}
			dir = "lit->gen"
			if res, err = mp.ToGen(fl, col); err != nil {
				return fail(err)
			}
		} else {
			g, err := mp.generatedName(fname)
			if err != nil {
				return fail(err)
			}
			if res, err = mp.ToLit(g, line, col); err != nil {
				return fail(err)
			}
		}
		any := false
		for _, r := range res {
			if r.Status == "" {
				any = true
			}
		}
		found = found && any
		report = append(report, entry{addr, dir, res})
	}
	if a.Has("json") {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", " ")
		for _, r := range report {
			if r.Results == nil {
				r.Results = []AddrResult{}
			}
		}
		enc.Encode(report)
		io.Out.Write(buf.Bytes())
	} else {
		many := len(report) > 1
		ind := ""
		if many {
			ind = "  "
		}
		for _, r := range report {
			if many {
				fmt.Fprintf(io.Out, "%s  [%s]\n", r.Address, r.Direction)
			}
			for _, res := range r.Results {
				fmt.Fprintln(io.Out, ind+renderAddr(res))
			}
			if len(r.Results) == 0 {
				fmt.Fprintln(io.Out, ind+"# no address: not code, or the chunk is never expanded into a file")
			}
		}
	}
	for _, n := range mp.Notes {
		fmt.Fprintln(io.Err, "note: "+n)
	}
	if !found {
		return 1
	}
	return 0
}
