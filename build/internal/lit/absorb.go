package lit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A diffHunk uses git's -U0 numbering: old lines A..A+B-1 (1-based) became new lines C..C+D-1; for B == 0 the
// lines were inserted after old line A.
type diffHunk struct {
	A, B, C, D     int
	Removed, Added []string
}

func diffHunks(old, new []string) []diffHunk {
	var out []diffHunk
	for _, h := range Hunks(old, new) {
		dh := diffHunk{A: h.A0 + 1, B: h.A1 - h.A0, C: h.B0 + 1, D: h.B1 - h.B0, Removed: old[h.A0:h.A1], Added: new[h.B0:h.B1]}
		if dh.B == 0 {
			dh.A = h.A0
		}
		out = append(out, dh)
	}
	return out
}

type lineKey struct{ block, line int }
type insKey struct {
	block, line int
	where       string
}
type lineEdit struct {
	text string
	del  bool
}

// A Plan collects edits to the program text, keyed by (block, line) so that a chunk expanded twice is edited once.
type Plan struct {
	m      *Map
	src    *Source
	org    map[string][]Origin
	lines  map[lineKey]lineEdit
	order  []lineKey
	ins    map[insKey][]string
	insOrd []insKey
	Errors []string
	Notes  []string
	Log    []string
}

func NewPlan(m *Map, src *Source) *Plan {
	p := &Plan{m: m, src: src, org: map[string][]Origin{}, lines: map[lineKey]lineEdit{}, ins: map[insKey][]string{}}
	for f, e := range m.Files {
		p.org[f] = e.Origins()
	}
	return p
}

func (p *Plan) where(bid, li int) string {
	blk := p.m.Blocks[bid]
	return fmt.Sprintf("\u00ab%s\u00bb, %s", blk.Chunk, p.src.Where(blk.Lines[li][0]))
}

// litText turns an edited generated line back into the text of the line in the program.
func (p *Plan) litText(bid int, text, pfx, fname string, lineno int) (string, bool) {
	if text == "" {
		return "", true
	}
	if !strings.HasPrefix(text, pfx) {
		p.Errors = append(p.Errors, fmt.Sprintf("%s:%d: indented less than the %d column(s) that <<<references>>> add at this place; change the reference in the .lit instead", fname, lineno, len(pfx)))
		return "", false
	}
	return p.m.Blocks[bid].Indent + text[len(pfx):], true
}

func (p *Plan) setLine(key lineKey, val lineEdit, fname string, lineno int) {
	if old, ok := p.lines[key]; ok && old != val {
		p.Errors = append(p.Errors, fmt.Sprintf("%s:%d: edits the .lit line at %s differently from another copy of the same chunk; make the copies agree", fname, lineno, p.where(key.block, key.line)))
		return
	}
	if _, ok := p.lines[key]; !ok {
		p.order = append(p.order, key)
	}
	p.lines[key] = val
}

func (p *Plan) replace(fname string, lineno int, text string) {
	o := p.org[fname][lineno-1]
	if t, ok := p.litText(o.Block, text, o.Prefix, fname, lineno); ok {
		p.setLine(lineKey{o.Block, o.Line}, lineEdit{text: t}, fname, lineno)
	}
}

func (p *Plan) delete(fname string, lineno int) {
	o := p.org[fname][lineno-1]
	p.setLine(lineKey{o.Block, o.Line}, lineEdit{del: true}, fname, lineno)
}

func (p *Plan) insert(fname string, a int, add []string) {
	org := p.org[fname]
	if len(org) == 0 {
		p.Errors = append(p.Errors, fmt.Sprintf("%s: no line of this file comes from the .lit, so there is nowhere to anchor an insertion; write the code into the .lit by hand", fname))
		return
	}
	var o Origin
	where := "before"
	if a >= 1 {
		o, where = org[a-1], "after"
		if a < len(org) && !(org[a].Block == o.Block && org[a].Line == o.Line+1) {
			p.Notes = append(p.Notes, fmt.Sprintf("%s: lines inserted after line %d sit at a chunk boundary; placed at the end of %s (they could also start %s)",
				fname, a, p.where(o.Block, o.Line), p.where(org[a].Block, org[a].Line)))
		}
	} else {
		o = org[0]
	}
	lines := make([]string, len(add))
	for i, t := range add {
		s, ok := p.litText(o.Block, t, o.Prefix, fname, a+1+i)
		if !ok {
			return
		}
		lines[i] = s
	}
	key := insKey{o.Block, o.Line, where}
	if prev, ok := p.ins[key]; ok {
		if strings.Join(prev, "\n") != strings.Join(lines, "\n") || len(prev) != len(lines) {
			p.Errors = append(p.Errors, fmt.Sprintf("%s: inserts different text than another copy of the same chunk at %s", fname, p.where(o.Block, o.Line)))
		}
		return
	}
	p.ins[key] = lines
	p.insOrd = append(p.insOrd, key)
}

// AbsorbFile plans the edits that turn the pristine lines of a generated file into the edited ones.
func (p *Plan) AbsorbFile(fname string, old, new []string) {
	for _, h := range diffHunks(old, new) {
		if h.B == 0 {
			p.insert(fname, h.A, h.Added)
			p.Log = append(p.Log, fmt.Sprintf("%s:%d+  %d line(s) added", fname, h.A, h.D))
			continue
		}
		for i := 0; i < min(h.B, h.D); i++ {
			p.replace(fname, h.A+i, h.Added[i])
		}
		if h.D > h.B {
			p.insert(fname, h.A+h.B-1, h.Added[h.B:])
		}
		for i := h.D; i < h.B; i++ {
			p.delete(fname, h.A+i)
		}
		o := p.org[fname][h.A-1]
		rng := fmt.Sprint(h.A)
		if h.B > 1 {
			rng = fmt.Sprintf("%d-%d", h.A, h.A+h.B-1)
		}
		msg := fmt.Sprintf("%s:%s  %s: %d changed", fname, rng, p.where(o.Block, o.Line), min(h.B, h.D))
		if h.D > h.B {
			msg += fmt.Sprintf(", %d added", h.D-h.B)
		}
		if h.D < h.B {
			msg += fmt.Sprintf(", %d removed", h.B-h.D)
		}
		p.Log = append(p.Log, msg)
	}
}

type spliceOp struct {
	start, end int
	text       string
	order      int
}

// Apply returns the program text with the planned edits spliced in, in a single forward pass.
func (p *Plan) Apply() (string, error) {
	var ops []spliceOp
	for _, key := range p.order {
		e := p.lines[key]
		blk := p.m.Blocks[key.block]
		start, end := blk.Lines[key.line][0], blk.Lines[key.line][1]
		if e.del {
			if key.line < len(blk.Lines)-1 || blk.NlLast {
				end++
			}
			ops = append(ops, spliceOp{start, end, "", 2})
		} else {
			ops = append(ops, spliceOp{start, end, e.text, 2})
		}
	}
	for _, key := range p.insOrd {
		lines := p.ins[key]
		blk := p.m.Blocks[key.block]
		start, end := blk.Lines[key.line][0], blk.Lines[key.line][1]
		switch {
		case key.where == "before":
			ops = append(ops, spliceOp{start, start, strings.Join(lines, "\n") + "\n", 1})
		case key.line < len(blk.Lines)-1 || blk.NlLast:
			ops = append(ops, spliceOp{end + 1, end + 1, strings.Join(lines, "\n") + "\n", 0})
		default:
			ops = append(ops, spliceOp{end, end, "\n" + strings.Join(lines, "\n"), 0})
		}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end < b.end
		}
		return a.order < b.order
	})
	var sb strings.Builder
	cur := 0
	text := p.src.Text
	for _, o := range ops {
		if o.start < cur {
			return "", fmt.Errorf("overlapping edits; edit fewer lines at once")
		}
		sb.WriteString(text[cur:o.start])
		sb.WriteString(o.text)
		cur = o.end
	}
	sb.WriteString(text[cur:])
	return sb.String(), nil
}

func shortDiff(actual, expected []string) []string {
	d := UnifiedDiff(actual, expected, "on disk", "regenerated from the patched .lit", 0)
	const limit = 14
	if len(d) > limit {
		return append(append([]string{}, d[:limit]...), fmt.Sprintf("... (%d more lines)", len(d)-limit))
	}
	return d
}

// ---- the command

var absorbSpec = Spec{Opts: map[string]bool{"outdir": true, "map": true, "dry-run": false, "force": false}, Short: map[string]string{"o": "outdir"}}

func Absorb(argv []string, io IO) int {
	a, err := ParseArgs(argv, absorbSpec)
	if err != nil || len(a.Pos) == 0 {
		fmt.Fprintln(io.Err, "usage: lit absorb SOURCE.lit [FILE ...] [-o OUTDIR] [--dry-run] [--force] [--map PATH]")
		return 2
	}
	if err := runAbsorb(a, io); err != nil {
		if e, ok := err.(exitCode); ok {
			return int(e)
		}
		fmt.Fprintln(io.Err, "absorb: "+err.Error())
		return 1
	}
	return 0
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func runAbsorb(a *Args, io IO) error {
	source, outdir := a.Pos[0], a.Get("outdir", ".")
	src, err := ReadSource(source)
	if err != nil {
		return err
	}
	text := src.Text
	m, err := LoadMap(a.Get("map", MapPath(source, outdir)))
	if err != nil {
		return err
	}
	if sha256hex(text) != m.SourceSha256 {
		return fmt.Errorf("%s has changed since it was tangled, so the sourcemap is stale. Tangle again (this discards edits to generated files), absorb before touching the .lit, or merge both sides with sync", source)
	}
	pristine, _, err := TangleText(text)
	if err != nil {
		return err
	}
	for _, f := range m.FileOrder {
		if sha256hex(pristine[f]) != m.Files[f].Sha256 {
			return fmt.Errorf("the sourcemap does not match what this tangle generates; tangle again")
		}
	}
	var targets []string
	if len(a.Pos) > 1 {
		for _, f := range a.Pos[1:] {
			rel := f
			if _, err := os.Stat(f); err == nil {
				if r, err := filepath.Rel(outdir, f); err == nil {
					rel = filepath.ToSlash(r)
				}
			}
			if _, ok := m.Files[rel]; !ok {
				return fmt.Errorf("%s is not generated from %s (known: %s)", f, source, strings.Join(m.FileOrder, ", "))
			}
			targets = append(targets, rel)
		}
	} else {
		targets = m.FileOrder
	}
	plan := NewPlan(m, src)
	var touched []string
	for _, f := range targets {
		path := filepath.Join(outdir, filepath.FromSlash(f))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		cur, err := ReadDisk(path)
		if err != nil {
			return err
		}
		old, new := ToLines(pristine[f]), ToLines(cur)
		if strings.Join(old, "\n") != strings.Join(new, "\n") || len(old) != len(new) {
			touched = append(touched, f)
			plan.AbsorbFile(f, old, new)
		}
	}
	if len(touched) == 0 {
		fmt.Fprintln(io.Out, "nothing to absorb: no generated file differs from what the .lit produces")
		return nil
	}
	for _, l := range plan.Log {
		fmt.Fprintln(io.Out, "  "+l)
	}
	for _, n := range plan.Notes {
		fmt.Fprintln(io.Out, "note: "+n)
	}
	if len(plan.Errors) > 0 {
		fmt.Fprintln(io.Out, "cannot absorb:\n  "+strings.Join(plan.Errors, "\n  "))
		return exitCode(1)
	}
	newText, err := plan.Apply()
	if err != nil {
		return err
	}
	regenerated, _, err := TangleText(newText)
	if err != nil {
		return fmt.Errorf("the patched .lit no longer parses (%v); an edited line probably contains the code fence delimiter", err)
	}
	problems := map[string][]string{}
	var problemOrder []string
	affected := map[string]bool{}
	for _, f := range touched {
		affected[f] = true
	}
	isTouched := func(f string) bool {
		for _, t := range touched {
			if t == f {
				return true
			}
		}
		return false
	}
	for _, f := range m.FileOrder {
		path := filepath.Join(outdir, filepath.FromSlash(f))
		reg, ok := regenerated[f]
		if !ok {
			problems[f] = []string{"no longer generated by the patched .lit"}
			problemOrder = append(problemOrder, f)
			continue
		}
		if isTouched(f) || !equalLines(ToLines(reg), ToLines(pristine[f])) {
			affected[f] = true
			if _, err := os.Stat(path); err == nil {
				disk, _ := ReadDisk(path)
				if !equalLines(ToLines(reg), ToLines(disk)) {
					problems[f] = shortDiff(ToLines(disk), ToLines(reg))
					problemOrder = append(problemOrder, f)
					if !isTouched(f) && !equalLines(ToLines(disk), ToLines(pristine[f])) {
						problems[f] = append(problems[f], "(this file also has edits you did not name; they would be overwritten)")
					}
				}
			}
		}
	}
	changed, err := src.SplitBack(newText)
	if err != nil {
		return err
	}
	var names []string
	for n := range changed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintln(io.Out, strings.Join(UnifiedDiff(strings.Split(src.Texts[n], "\n"), strings.Split(changed[n], "\n"), "a/"+n, "b/"+n, 1), "\n"))
	}
	if len(problems) > 0 {
		fmt.Fprintln(io.Out, "\nside effects: after absorbing, the .lit would regenerate these files differently from what is on disk")
		for _, f := range problemOrder {
			fmt.Fprintf(io.Out, "  %s:\n    %s\n", f, strings.Join(problems[f], "\n    "))
		}
		if !a.Has("force") {
			fmt.Fprintln(io.Out, "\nnothing written. Usually a chunk is used in several places: make the same edit everywhere (or name the other files too), or pass --force to accept the .lit's version.")
			return exitCode(1)
		}
	}
	if a.Has("dry-run") {
		fmt.Fprintln(io.Out, "\n(dry run: nothing written)")
		return nil
	}
	if _, err := src.WriteBack(newText); err != nil {
		return err
	}
	var aff []string
	for f := range affected {
		if _, ok := regenerated[f]; ok {
			aff = append(aff, f)
		}
	}
	sort.Strings(aff)
	for _, f := range aff {
		path, content := filepath.Join(outdir, filepath.FromSlash(f)), regenerated[f]
		disk, err := ReadDisk(path)
		if err != nil || !equalLines(ToLines(disk), ToLines(content)) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		}
	}
	if _, err := writeMap(source, outdir, true); err != nil {
		return err
	}
	msg := fmt.Sprintf("\nabsorbed edits from %d file(s) into %s; sourcemap refreshed", len(touched), source)
	if len(problems) > 0 {
		msg += "; regenerated: " + strings.Join(problemOrder, ", ")
	}
	fmt.Fprintln(io.Out, msg)
	return nil
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
