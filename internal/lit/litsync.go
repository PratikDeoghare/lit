package lit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Decisions a person (or a flag) can make about one region.
const (
	Apply   = "apply"
	Leave   = "leave"
	Revert  = "revert"
	TakeLit = "lit"
	TakeGen = "gen"
	Both    = "both"
	Custom  = "custom"
)

// ---- the store: <outdir>/<source>.sync/

type Store struct {
	Source, Outdir, Dir string
}

func NewStore(source, outdir string) *Store {
	return &Store{source, outdir, filepath.Join(outdir, filepath.Base(source)+".sync")}
}

func (s *Store) p(parts ...string) string { return filepath.Join(append([]string{s.Dir}, parts...)...) }

func (s *Store) HasBase() bool { _, err := os.Stat(s.p("state.json")); return err == nil }

type State struct {
	Files    []string          `json:"files"`
	LitSha   string            `json:"lit_sha"`
	LitFiles map[string]string `json:"lit_files"`
	T        float64           `json:"t"`
	Note     string            `json:"note"`
}

func (s *Store) State() State {
	var st State
	if b, err := os.ReadFile(s.p("state.json")); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func (s *Store) BaseFiles() map[string]string {
	out := map[string]string{}
	for _, n := range s.State().Files {
		t, _ := ReadDisk(s.p("base", n))
		out[n] = t
	}
	return out
}

// BaseLit is the program text at the last sync (the whole text, or one chapter file when name is given).
func (s *Store) BaseLit(name string) (string, bool) {
	if name != "" {
		if t, err := ReadDisk(s.p("base_lit", name)); err == nil {
			return t, true
		}
	}
	t, err := ReadDisk(s.p("base.lit"))
	return t, err == nil
}

func writeFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func (s *Store) SetBase(files map[string]string, lit, note string, parts map[string]string) error {
	if parts == nil {
		parts = map[string]string{filepath.Base(s.Source): lit}
	}
	os.RemoveAll(s.p("base"))
	os.RemoveAll(s.p("base_lit"))
	for n, t := range parts {
		if err := writeFile(s.p("base_lit", n), t); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(files))
	for n, t := range files {
		if err := writeFile(s.p("base", n), t); err != nil {
			return err
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if err := writeFile(s.p("base.lit"), lit); err != nil {
		return err
	}
	lf := map[string]string{}
	for n, t := range parts {
		lf[n] = sha256hex(t)
	}
	b, _ := json.Marshal(State{names, sha256hex(lit), lf, now(), note})
	if err := writeFile(s.p("state.json"), string(b)); err != nil {
		return err
	}
	s.Snapshot(lit)
	pn := make([]string, 0, len(parts))
	for n := range parts {
		pn = append(pn, n)
	}
	sort.Strings(pn)
	for _, n := range pn {
		t := parts[n]
		s.Log("base", "lit", n, &t, map[string]any{"note": note})
	}
	return nil
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func (s *Store) Snapshot(text string) string {
	h := sha256hex(text)
	if _, err := os.Stat(s.p("snapshots", h)); err != nil {
		writeFile(s.p("snapshots", h), text)
	}
	return h
}

func (s *Store) ReadSnapshot(prefix string) (string, string, error) {
	ents, _ := os.ReadDir(s.p("snapshots"))
	var hits []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) {
			hits = append(hits, e.Name())
		}
	}
	if len(hits) != 1 {
		return "", "", fmt.Errorf("snapshot %q matches %d recorded versions", prefix, len(hits))
	}
	t, err := ReadDisk(s.p("snapshots", hits[0]))
	return hits[0], t, err
}

// Event is one line of the journal.
type Event struct {
	T     float64  `json:"t"`
	Kind  string   `json:"kind"`
	Side  string   `json:"side"`
	Path  string   `json:"path"`
	Sha   string   `json:"sha"`
	Note  string   `json:"note,omitempty"`
	Files []string `json:"files,omitempty"`
	Prev  string   `json:"prev,omitempty"`
}

func (s *Store) Log(kind, side, path string, text *string, extra map[string]any) Event {
	ev := Event{T: now(), Kind: kind, Side: side, Path: path}
	if text != nil {
		ev.Sha = s.Snapshot(*text)
	}
	if v, ok := extra["note"].(string); ok {
		ev.Note = v
	}
	if v, ok := extra["prev"].(string); ok {
		ev.Prev = v
	}
	if v, ok := extra["files"].([]string); ok {
		ev.Files = v
	}
	os.MkdirAll(s.Dir, 0o755)
	if f, err := os.OpenFile(s.p("journal.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		b, _ := json.Marshal(ev)
		f.Write(append(b, '\n'))
		f.Close()
	}
	return ev
}

func (s *Store) Events() []Event {
	b, err := os.ReadFile(s.p("journal.jsonl"))
	if err != nil {
		return nil
	}
	var out []Event
	for _, l := range strings.Split(string(b), "\n") {
		var e Event
		if strings.TrimSpace(l) != "" && json.Unmarshal([]byte(l), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) LastEdit(side, path string) (float64, bool) {
	var t float64
	ok := false
	for _, e := range s.Events() {
		if e.Kind == "edit" && e.Side == side && e.Path == path && e.T > t {
			t, ok = e.T, true
		}
	}
	return t, ok
}

// protect is called by tangle before it overwrites a generated file: it keeps a copy of edits the .lit lacks.
func protect(source, outdir, name, path, newText string) {
	cur, err := ReadDisk(path)
	if err != nil || cur == newText {
		return
	}
	st := NewStore(source, outdir)
	var known string
	have := false
	if st.HasBase() {
		known, have = st.BaseFiles()[name]
	} else if m, err := LoadMap(MapPath(source, outdir)); err == nil {
		if e, ok := m.Files[name]; ok && e.Sha256 == sha256hex(cur) {
			known, have = cur, true
		}
	}
	if have && cur == known {
		return
	}
	h := st.Log("backup", "gen", name, &cur, map[string]any{"note": "replaced by tangle"}).Sha
	fmt.Fprintf(os.Stderr, "warning: %s had edits that are not in the .lit; tangle replaced them, but a copy is kept (snapshot %s; see lit sync %s log / restore)\n", path, h[:8], source)
}

// ---- three-way regions

type Region struct {
	S, E           int // base lines [S, E) are affected
	Base, Lit, Gen []string
	Kind           string // "lit" | "gen" | "same" | "conflict"
}

type span struct {
	s, e int
	new  []string
	who  byte
}

func spans(old, new []string, who byte) []span {
	if equalLines(old, new) {
		return nil
	}
	var out []span
	for _, h := range diffHunks(old, new) {
		s := h.A
		if h.B != 0 {
			s = h.A - 1
		}
		out = append(out, span{s, s + h.B, h.Added, who})
	}
	return out
}

type group struct {
	s, e int
	h    []span
}

func overlap(g *group, h span) bool {
	if h.s == h.e && h.s == g.s && g.s == g.e {
		return true
	}
	return h.s < g.e && g.s < h.e
}

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Diff3 finds the regions where ours (the .lit side) or theirs (the file) differ from base.
func Diff3(base, ours, theirs []string) []Region {
	items := append(spans(base, ours, 'o'), spans(base, theirs, 't')...)
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.s != b.s {
			return a.s < b.s
		}
		if a.e != b.e {
			return a.e < b.e
		}
		return a.who < b.who
	})
	var groups []*group
	for _, it := range items {
		if n := len(groups); n > 0 && overlap(groups[n-1], it) {
			g := groups[n-1]
			g.s, g.e = min(g.s, it.s), max(g.e, it.e)
			g.h = append(g.h, it)
		} else {
			groups = append(groups, &group{it.s, it.e, []span{it}})
		}
	}
	var regions []Region
	for _, g := range groups {
		side := func(which byte) []string {
			var out []string
			pos := g.s
			hs := append([]span(nil), g.h...)
			sort.SliceStable(hs, func(i, j int) bool {
				if hs[i].s != hs[j].s {
					return hs[i].s < hs[j].s
				}
				return hs[i].e < hs[j].e
			})
			for _, h := range hs {
				if h.who == which {
					out = cat(out, base[pos:h.s], h.new)
					pos = h.e
				}
			}
			return cat(out, base[pos:g.e])
		}
		hasO, hasT := false, false
		for _, h := range g.h {
			hasO = hasO || h.who == 'o'
			hasT = hasT || h.who == 't'
		}
		lit, gen := side('o'), side('t')
		kind := "conflict"
		switch {
		case !hasT:
			kind = "lit"
		case !hasO:
			kind = "gen"
		case equalLines(lit, gen):
			kind = "same"
		}
		regions = append(regions, Region{g.s, g.e, base[g.s:g.e], lit, gen, kind})
	}
	return regions
}

type Decision struct {
	Act    string
	Custom []string
}

func DefaultDecision(r Region) Decision {
	if r.Kind == "conflict" {
		return Decision{Act: Leave}
	}
	return Decision{Act: Apply}
}

// Outcome of one decision: what the .lit side will yield, what the file will hold, what the base becomes.
func outcomeOf(r Region, d Decision) (l, g, b []string, err error) {
	same := func(x []string) ([]string, []string, []string, error) { return x, x, x, nil }
	switch d.Act {
	case Apply:
		if r.Kind == "conflict" {
			return nil, nil, nil, errors.New("a conflict has to be resolved, not applied")
		}
		if r.Kind == "lit" || r.Kind == "same" {
			return same(r.Lit)
		}
		return same(r.Gen)
	case Revert:
		return same(r.Base)
	case Leave:
		return r.Lit, r.Gen, r.Base, nil
	case TakeLit:
		return same(r.Lit)
	case TakeGen:
		return same(r.Gen)
	case Both:
		return same(cat(r.Lit, r.Gen))
	case Custom:
		return same(d.Custom)
	}
	return nil, nil, nil, fmt.Errorf("unknown decision %q", d.Act)
}

// ---- analysis

type FileState struct {
	Name            string
	Base, Ours, Gen []string
	Missing         bool
	Regions         []Region
}

type Analysis struct {
	Source, Outdir string
	Store          *Store
	Src            *Source
	LitText        string
	Ours           map[string]string
	Base           map[string]string
	Files          map[string]*FileState
	Names          []string
}

func NewAnalysis(source, outdir string) (*Analysis, error) {
	src, err := ReadSource(source)
	if err != nil {
		return nil, err
	}
	an := &Analysis{Source: source, Outdir: outdir, Store: NewStore(source, outdir), Src: src, LitText: src.Text, Files: map[string]*FileState{}}
	an.Ours, _, err = TangleText(src.Text)
	if err != nil {
		return nil, err
	}
	if an.Base, err = an.base(); err != nil {
		return nil, err
	}
	for n := range an.Ours {
		an.Names = append(an.Names, n)
	}
	sort.Strings(an.Names)
	for _, n := range an.Names {
		path := filepath.Join(outdir, n)
		_, serr := os.Stat(path)
		missing := serr != nil
		B, O := ToLines(an.Base[n]), ToLines(an.Ours[n])
		T := O
		if !missing {
			t, _ := ReadDisk(path)
			T = ToLines(t)
		}
		an.Files[n] = &FileState{n, B, O, T, missing, Diff3(B, O, T)}
	}
	return an, nil
}

func (an *Analysis) base() (map[string]string, error) {
	if an.Store.HasBase() {
		return an.Store.BaseFiles(), nil
	}
	m, err := LoadMap(MapPath(an.Source, an.Outdir))
	if err != nil {
		return nil, errors.New("no sync base yet: run lit tangle once (it records one)")
	}
	if sha256hex(an.LitText) == m.SourceSha256 {
		return an.Ours, nil
	}
	return nil, errors.New("no sync base was recorded and the .lit changed since the last tangle; give the .lit the generated files came from: lit sync adopt --base-lit OLD.lit (or --gen-is-base if you never edited the generated files)")
}

func (an *Analysis) LitChanged() bool { return sha256hex(an.LitText) != an.Store.State().LitSha }

// ---- applying decisions

type Outcome struct {
	NewLit     string
	GenText    map[string]string
	BaseText   map[string]string
	Ripple     map[string][2][]string
	Notes      []string
	Errors     []string
	LitChanged bool
}

type RegionKey struct {
	File string
	I    int
}

// Prepare computes the result of the decisions; it writes nothing.
func Prepare(an *Analysis, decisions map[RegionKey]Decision) (*Outcome, error) {
	L, G, B := map[string][]string{}, map[string][]string{}, map[string][]string{}
	for _, name := range an.Names {
		fs := an.Files[name]
		var l, g, b []string
		pos := 0
		for i, r := range fs.Regions {
			d, ok := decisions[RegionKey{name, i}]
			if !ok {
				d = DefaultDecision(r)
			}
			lt, gt, bt, err := outcomeOf(r, d)
			if err != nil {
				return nil, err
			}
			stable := fs.Base[pos:r.S]
			l, g, b = cat(l, stable, lt), cat(g, stable, gt), cat(b, stable, bt)
			pos = r.E
		}
		stable := fs.Base[pos:]
		L[name], G[name], B[name] = cat(l, stable), cat(g, stable), cat(b, stable)
	}
	out := &Outcome{NewLit: an.LitText, Ripple: map[string][2][]string{}}
	var need []string
	for _, n := range an.Names {
		if !equalLines(L[n], an.Files[n].Ours) {
			need = append(need, n)
		}
	}
	if len(need) > 0 {
		m, _, err := BuildMap(an.Src)
		if err != nil {
			return nil, err
		}
		plan := NewPlan(m, an.Src)
		for _, n := range need {
			plan.AbsorbFile(n, an.Files[n].Ours, L[n])
		}
		out.Errors, out.Notes = plan.Errors, plan.Notes
		if len(out.Errors) == 0 {
			if out.NewLit, err = plan.Apply(); err != nil {
				return nil, err
			}
		}
	}
	regen := an.Ours
	if len(out.Errors) == 0 {
		var err error
		if regen, _, err = TangleText(out.NewLit); err != nil {
			return nil, err
		}
	}
	for _, n := range an.Names {
		got := ToLines(regen[n])
		if len(out.Errors) == 0 && !equalLines(got, L[n]) {
			out.Ripple[n] = [2][]string{L[n], got}
			G[n], B[n] = got, got
		}
	}
	out.GenText, out.BaseText = map[string]string{}, map[string]string{}
	for _, n := range an.Names {
		out.GenText[n], out.BaseText[n] = FileBody(G[n]), FileBody(B[n])
	}
	out.LitChanged = out.NewLit != an.LitText
	return out, nil
}

// Commit writes an Outcome: backs up what is about to change, writes the .lit and files, records the new base.
func Commit(an *Analysis, out *Outcome, note string) ([]string, error) {
	st := an.Store
	if out.LitChanged {
		changed, err := an.Src.SplitBack(out.NewLit)
		if err != nil {
			return nil, err
		}
		for n := range changed {
			t := an.Src.Texts[n]
			st.Log("backup", "lit", n, &t, map[string]any{"note": "before " + note})
		}
		if _, err := an.Src.WriteBack(out.NewLit); err != nil {
			return nil, err
		}
	}
	var written []string
	for _, n := range an.Names {
		text := out.GenText[n]
		path := filepath.Join(an.Outdir, n)
		fs := an.Files[n]
		var cur *string
		if !fs.Missing {
			c, _ := ReadDisk(path)
			cur = &c
		}
		if cur != nil && equalLines(ToLines(*cur), ToLines(text)) {
			continue
		}
		if cur != nil && !equalLines(ToLines(*cur), fs.Base) {
			st.Log("backup", "gen", n, cur, map[string]any{"note": "before " + note})
		}
		if err := writeFile(path, text); err != nil {
			return nil, err
		}
		written = append(written, n)
	}
	if _, err := writeMap(an.Source, an.Outdir, false); err != nil {
		return nil, err
	}
	// the chapter texts as they are now on disk
	src, err := ReadSource(an.Source)
	if err != nil {
		return nil, err
	}
	if err := st.SetBase(out.BaseText, out.NewLit, note, src.Texts); err != nil {
		return nil, err
	}
	st.Log(note, "lit", filepath.Base(an.Source), nil, map[string]any{"files": written})
	return written, nil
}
