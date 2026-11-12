package lit

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func apply(a, b []string, hs []Hunk) []string {
	var out []string
	at := 0
	for _, h := range hs {
		out = append(out, a[at:h.A0]...)
		out = append(out, b[h.B0:h.B1]...)
		at = h.A1
	}
	return append(out, a[at:]...)
}

func TestHunksRebuildTheNewText(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	words := strings.Fields("a b c d e f g")
	for i := 0; i < 400; i++ {
		var a, b []string
		for j := 0; j < r.Intn(14); j++ {
			a = append(a, words[r.Intn(len(words))])
		}
		for j := 0; j < r.Intn(14); j++ {
			b = append(b, words[r.Intn(len(words))])
		}
		hs := Hunks(a, b)
		if got := apply(a, b, hs); !reflect.DeepEqual(got, b) && !(len(got) == 0 && len(b) == 0) {
			t.Fatalf("a=%v b=%v hunks=%v rebuilt=%v", a, b, hs, got)
		}
		for k := 1; k < len(hs); k++ {
			if hs[k].A0 <= hs[k-1].A1 && hs[k].B0 <= hs[k-1].B1 {
				t.Fatalf("hunks touch: %v", hs)
			}
		}
	}
}

func TestHunksAreSmallForSmallEdits(t *testing.T) {
	a := strings.Split(strings.Repeat("x\n", 50)+"old\n"+strings.Repeat("y\n", 50), "\n")
	b := strings.Split(strings.Repeat("x\n", 50)+"new\n"+strings.Repeat("y\n", 50), "\n")
	hs := Hunks(a, b)
	if len(hs) != 1 || hs[0] != (Hunk{50, 51, 50, 51}) {
		t.Fatalf("%v", hs)
	}
}
