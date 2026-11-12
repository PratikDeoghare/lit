package lit

import (
	"fmt"
	"strings"
)

// A HintSite is an #agent-fill-in{...} bag inside a chunk, with its place in the program text.
type HintSite struct {
	Index      int    // number among the hints of the program, in document order (the Index of the Hole)
	Chunk      string // the chunk it sits in
	Start, End int    // byte range of the whole bag in Source.Text
	Text       string // the hint as plain text
	Raw        string // the text between the braces, as written
	File       string // source file (the root or a chapter) and the line in it
	Line       int
}

// FindHints lists the hints of a program in document order. They are numbered exactly as Collect numbers its
// holes, so site i and hole i are the same hint.
func FindHints(src *Source, root Bag) []HintSite {
	var out []HintSite
	Walk(root, func(n Node, parents []Bag, start, end int) bool {
		b, ok := n.(Bag)
		if !ok {
			return true
		}
		tag := strings.TrimSpace(b.Tag())
		if b.Tag() == HintTag {
			// a hint counts when every bag around it is a chunk (as in Collect) and there is one
			for _, p := range parents[1:] {
				if Reserved[strings.TrimSpace(p.Tag())] {
					return false
				}
			}
			if len(parents) < 2 {
				return false
			}
			name, line, _ := src.Locate(start)
			out = append(out, HintSite{Index: len(out), Chunk: strings.TrimSpace(parents[len(parents)-1].Tag()),
				Start: start, End: end, Text: PlainText(b), Raw: src.Text[start+len(HintTag)+2 : end-1], File: name, Line: line})
			return false
		}
		return !Reserved[tag]
	})
	return out
}

// Describe is the hint as one line for lists.
func (h HintSite) Describe() string {
	t := h.Text
	if t == "" {
		t = "(no description)"
	}
	return fmt.Sprintf("%s:%d  «%s»  %s", h.File, h.Line, h.Chunk, trunc(t, 80))
}
