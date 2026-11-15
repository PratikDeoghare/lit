package lit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/pratikdeoghare/brashtag-literate/internal/brashtag"
)

// A Piece says that Text[Start:End] came from file Name, starting at byte Ofs of that file.
type Piece struct {
	Start, End int
	Name       string
	Ofs        int
}

// Source is a literate program made of one or more files, seen as one text.
//
// "#include{chapter.lit}" anywhere in a file is replaced by the text of that file (relative to the including
// file), so every tool works on one flat text, Text, and Pieces remembers which stretch of it came from which
// file. Edits to the flat text are carried back to the files by SplitBack and WriteBack.
type Source struct {
	Root   string            // the root file, as given
	Base   string            // its directory
	Texts  map[string]string // file name (relative to Base, slashes) -> its text
	Order  []string          // the files, root first, in the order they were read
	Pieces []Piece
	Text   string
}

type reader func(path string) (string, error)

func diskReader(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// ReadSource reads the root file and everything it includes.
func ReadSource(path string) (*Source, error) { return readSource(path, diskReader) }

func readSource(path string, read reader) (*Source, error) {
	s := &Source{Root: filepath.Clean(path), Texts: map[string]string{}}
	s.Base = filepath.Dir(s.Root)
	var parts []part
	if err := s.expand(s.Root, nil, &parts, read); err != nil {
		return nil, err
	}
	var sb strings.Builder
	for _, p := range parts {
		s.Pieces = append(s.Pieces, Piece{sb.Len(), sb.Len() + len(p.text), p.name, p.ofs})
		sb.WriteString(p.text)
	}
	s.Text = sb.String()
	return s, nil
}

type part struct {
	text, name string
	ofs        int
}

// Rel names a path of the program relative to the root's directory, with forward slashes.
func (s *Source) Rel(path string) string {
	r, err := filepath.Rel(s.Base, path)
	if err != nil {
		r = path
	}
	return filepath.ToSlash(r)
}

func (s *Source) expand(path string, stack []string, parts *[]part, read reader) error {
	name := s.Rel(path)
	for _, p := range stack {
		if p == path {
			var names []string
			for _, q := range append(append([]string{}, stack...), path) {
				names = append(names, s.Rel(q))
			}
			return fmt.Errorf("recursive #include: %s", strings.Join(names, " -> "))
		}
	}
	if _, dup := s.Texts[name]; dup {
		return fmt.Errorf("%s is included twice; each chapter file may appear once (use <<<chunk>>> references to share code)", name)
	}
	text, err := read(path)
	if err != nil {
		who := name
		if len(stack) > 0 {
			who = s.Rel(stack[len(stack)-1])
		}
		return fmt.Errorf("%s: cannot read %s: %v", who, path, unwrap(err))
	}
	if strings.Contains(text, "\r") {
		return fmt.Errorf("%s: carriage returns found; convert it to LF line endings (the sourcemap uses offsets)", path)
	}
	s.Texts[name] = text
	s.Order = append(s.Order, name)
	incs, err := includes(text, name)
	if err != nil {
		return err
	}
	at := 0
	for _, inc := range incs {
		*parts = append(*parts, part{text[at:inc.start], name, at})
		sub := filepath.Clean(filepath.Join(filepath.Dir(path), inc.path))
		if _, err := read(sub); err != nil {
			return fmt.Errorf("%s:%d: #include{%s}: no such file", name, inc.line, inc.path)
		}
		if err := s.expand(sub, append(append([]string{}, stack...), path), parts, read); err != nil {
			return err
		}
		at = inc.end
	}
	*parts = append(*parts, part{text[at:], name, at})
	return nil
}

func unwrap(err error) string {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

type include struct {
	start, end int
	path       string
	line       int
}

// includes finds the #include{path} bags of one file. They are found in the parse tree, not with a regular
// expression, so one inside code is never mistaken for a directive; the walk does not look inside them.
func includes(text, name string) ([]include, error) {
	root, err := ParseText(text, func(off int) string {
		return fmt.Sprintf("%s:%d", name, strings.Count(text[:min(off, len(text))], "\n")+1)
	})
	if err != nil {
		return nil, err
	}
	var found []include
	var bad error
	Walk(root, func(n brashtag.Node, parents []brashtag.Bag, start, end int) bool {
		b, ok := n.(brashtag.Bag)
		if !ok || b.Tag() != "include" {
			return true
		}
		var sb strings.Builder
		for _, k := range b.Kids() {
			bl, ok := k.(brashtag.Blob)
			if !ok {
				bad = fmt.Errorf("%s:%d: #include{} wants a file name", name, strings.Count(text[:start], "\n")+1)
				return false
			}
			sb.WriteString(bl.Text())
		}
		p := strings.TrimSpace(sb.String())
		if p == "" {
			bad = fmt.Errorf("%s:%d: #include{} wants a file name", name, strings.Count(text[:start], "\n")+1)
			return false
		}
		found = append(found, include{start, end, p, strings.Count(text[:start], "\n") + 1})
		return false
	})
	return found, bad
}

// ---- positions

func (s *Source) piece(off int) Piece {
	for _, p := range s.Pieces {
		if p.Start <= off && off < p.End {
			return p
		}
	}
	if n := len(s.Pieces); n > 0 {
		return s.Pieces[n-1]
	}
	return Piece{Name: s.Rel(s.Root)}
}

// Locate maps an offset in the flat text to a file, a line and a column (both 1-based, columns in characters).
func (s *Source) Locate(off int) (name string, line, col int) {
	p := s.piece(off)
	at := p.Ofs + (off - p.Start)
	text := s.Texts[p.Name]
	if at > len(text) {
		at = len(text)
	}
	line = strings.Count(text[:at], "\n") + 1
	col = utf8.RuneCountInString(text[strings.LastIndex(text[:at], "\n")+1:at]) + 1
	return p.Name, line, col
}

// Where says "file:line" for an offset of the flat text.
func (s *Source) Where(off int) string {
	n, l, _ := s.Locate(off)
	return fmt.Sprintf("%s:%d", n, l)
}

// FlatOffset maps (file, line, column) to an offset of the flat text; ok is false if that spot is not part of it
// (the line of an #include directive, say).
func (s *Source) FlatOffset(name string, line, col int) (int, bool) {
	text, known := s.Texts[name]
	if !known {
		return 0, false
	}
	at := 0
	for i, l := range strings.Split(text, "\n") {
		if i == line-1 {
			break
		}
		at += len(l) + 1
	}
	at += col - 1
	for _, p := range s.Pieces {
		if p.Name == name && p.Ofs <= at && at < p.Ofs+(p.End-p.Start) {
			return p.Start + at - p.Ofs, true
		}
	}
	return 0, false
}

// Parse parses the flat text.
func (s *Source) Parse() (brashtag.Bag, error) { return ParseText(s.Text, s.Where) }

// ---- writing back

func splitKeep(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

type edit struct {
	a0, a1 int
	rep    string
}

// SplitBack returns the new text of every file that changes when the flat text becomes newText. The edits are
// found with a line diff and cut at the seams between files; the result is read again and must flatten to
// exactly newText, so an edit that would add, remove or move an #include is refused rather than guessed.
// Text inserted exactly on a seam goes to the end of the earlier file.
func (s *Source) SplitBack(newText string) (map[string]string, error) {
	old := s.Text
	if newText == old {
		return nil, nil
	}
	la, lb := splitKeep(old), splitKeep(newText)
	ca, cb := cum(la), cum(lb)
	var edits []edit
	for _, h := range Hunks(la, lb) {
		a0, a1, rep := ca[h.A0], ca[h.A1], newText[cb[h.B0]:cb[h.B1]]
		straddle := false
		for _, p := range s.Pieces {
			if a0 < p.Start && p.Start < a1 {
				straddle = true
			}
		}
		if !straddle {
			edits = append(edits, edit{a0, a1, rep})
			continue
		}
		ra, rb := runes(old[a0:a1]), runes(rep)
		pa := cum(ra)
		for _, h2 := range Hunks(ra, rb) {
			edits = append(edits, edit{a0 + pa[h2.A0], a0 + pa[h2.A1], strings.Join(rb[h2.B0:h2.B1], "")})
		}
	}
	per := map[string][]edit{}
	for _, e := range edits {
		if e.a0 == e.a1 { // an insertion belongs to the file that ends there
			var pc *Piece
			for i := range s.Pieces {
				if p := &s.Pieces[i]; p.Start < e.a0 && e.a0 <= p.End {
					pc = p
					break
				}
			}
			if pc == nil {
				for i := range s.Pieces {
					if p := &s.Pieces[i]; p.Start == e.a0 {
						pc = p
						break
					}
				}
			}
			per[pc.Name] = append(per[pc.Name], edit{pc.Ofs + e.a0 - pc.Start, pc.Ofs + e.a0 - pc.Start, e.rep})
			continue
		}
		first := true
		for _, p := range s.Pieces {
			lo, hi := max(e.a0, p.Start), min(e.a1, p.End)
			if lo < hi {
				rep := ""
				if first {
					rep = e.rep
				}
				per[p.Name] = append(per[p.Name], edit{p.Ofs + lo - p.Start, p.Ofs + hi - p.Start, rep})
				first = false
			}
		}
	}
	changed := map[string]string{}
	for name, eds := range per {
		text, shift := s.Texts[name], 0
		sortEdits(eds)
		for _, e := range eds {
			text = text[:e.a0+shift] + e.rep + text[e.a1+shift:]
			shift += len(e.rep) - (e.a1 - e.a0)
		}
		changed[name] = text
	}
	check, err := readSource(s.Root, func(path string) (string, error) {
		if t, ok := changed[s.Rel(path)]; ok {
			return t, nil
		}
		return diskReaderOr(s, path)
	})
	if err != nil || check.Text != newText {
		return nil, fmt.Errorf("the edit cannot be carried back to the chapter files (it adds, removes or moves an #include); change the #include lines in the files themselves")
	}
	return changed, nil
}

func diskReaderOr(s *Source, path string) (string, error) {
	if t, ok := s.Texts[s.Rel(path)]; ok {
		return t, nil
	}
	return diskReader(path)
}

func sortEdits(e []edit) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && (e[j].a0 < e[j-1].a0 || (e[j].a0 == e[j-1].a0 && e[j].a1 < e[j-1].a1)); j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
}

func cum(parts []string) []int {
	out := make([]int, len(parts)+1)
	for i, p := range parts {
		out[i+1] = out[i] + len(p)
	}
	return out
}

func runes(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

// WriteBack writes the files that change when the flat text becomes newText and returns old and new text of each.
func (s *Source) WriteBack(newText string) (map[string][2]string, error) {
	changed, err := s.SplitBack(newText)
	if err != nil {
		return nil, err
	}
	done := map[string][2]string{}
	for name, text := range changed {
		path := filepath.Join(s.Base, filepath.FromSlash(name))
		tmp := path + ".lit.tmp"
		if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return nil, err
		}
		done[name] = [2]string{s.Texts[name], text}
	}
	n, err := ReadSource(s.Root)
	if err != nil {
		return nil, err
	}
	*s = *n
	return done, nil
}
