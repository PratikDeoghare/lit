package lit

import (
	"fmt"
	"os"
	"strings"
)

// ToLines splits text into lines, without the empty piece after a final newline.
func ToLines(text string) []string {
	if text == "" {
		return nil
	}
	l := strings.Split(text, "\n")
	if l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}

// ReadDisk reads a text file and turns CRLF (and lone CR) into LF, so an editor's line endings do not matter.
func ReadDisk(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n"), nil
}

func fmtRange(begin, end int) string {
	start, length := begin+1, end-begin
	if length == 1 {
		return fmt.Sprint(start)
	}
	if length == 0 {
		start--
	}
	return fmt.Sprintf("%d,%d", start, length)
}

// UnifiedDiff renders a unified diff with n lines of context, in the format of difflib and diff -u.
func UnifiedDiff(a, b []string, fromName, toName string, n int) []string {
	hs := Hunks(a, b)
	if len(hs) == 0 {
		return nil
	}
	out := []string{"--- " + fromName, "+++ " + toName}
	for i := 0; i < len(hs); {
		j := i
		for j+1 < len(hs) && hs[j+1].A0-hs[j].A1 <= 2*n {
			j++
		}
		a0, b0 := max(hs[i].A0-n, 0), max(hs[i].B0-n, 0)
		a1, b1 := min(hs[j].A1+n, len(a)), min(hs[j].B1+n, len(b))
		out = append(out, fmt.Sprintf("@@ -%s +%s @@", fmtRange(a0, a1), fmtRange(b0, b1)))
		at := a0
		for k := i; k <= j; k++ {
			h := hs[k]
			for ; at < h.A0; at++ {
				out = append(out, " "+a[at])
			}
			for _, l := range a[h.A0:h.A1] {
				out = append(out, "-"+l)
			}
			for _, l := range b[h.B0:h.B1] {
				out = append(out, "+"+l)
			}
			at = h.A1
		}
		for ; at < a1; at++ {
			out = append(out, " "+a[at])
		}
		i = j + 1
	}
	return out
}
