package lit

// A Hunk says that a[A0:A1] became b[B0:B1]. Hunks are in order and never touch each other.
type Hunk struct{ A0, A1, B0, B1 int }

// Hunks returns the differences between two lists of lines. Equal lines at both ends are trimmed first,
// and Myers' O(ND) algorithm runs on the middle, so a few edits in a long file are cheap.
func Hunks(a, b []string) []Hunk {
	lo := 0
	for lo < len(a) && lo < len(b) && a[lo] == b[lo] {
		lo++
	}
	ha, hb := len(a), len(b)
	for ha > lo && hb > lo && a[ha-1] == b[hb-1] {
		ha--
		hb--
	}
	if lo == ha && lo == hb {
		return nil
	}
	return merge(myers(a[lo:ha], b[lo:hb], lo))
}

// merge joins hunks that touch: a deletion followed by an insertion is one replacement.
func merge(hs []Hunk) []Hunk {
	var out []Hunk
	for _, h := range hs {
		if n := len(out); n > 0 && h.A0 <= out[n-1].A1 && h.B0 <= out[n-1].B1 {
			out[n-1].A1, out[n-1].B1 = h.A1, h.B1
			continue
		}
		out = append(out, h)
	}
	return out
}

func myers(a, b []string, shift int) []Hunk {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return []Hunk{{shift, shift + n, shift, shift + m}}
	}
	max := n + m
	off := max
	v := make([]int, 2*max+2)
	var trace [][]int
	found := false
	for d := 0; d <= max && !found; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				trace = append(trace, nil)
				break
			}
		}
	}
	// walk back through the saved frontiers, collecting the matching diagonal runs
	type step struct{ x0, y0, x1, y1 int }
	var snakes []step
	x, y := n, m
	for d := len(trace) - 2; d >= 0; d-- {
		vv := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && vv[off+k-1] < vv[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := vv[off+pk]
		py := px - pk
		// the edit leaves (px,py) and reaches (sx,sy), then the diagonal continues to (x,y)
		var sx, sy int
		if pk == k+1 { // insertion of b[py]
			sx, sy = px, py+1
		} else { // deletion of a[px]
			sx, sy = px+1, py
		}
		if d == 0 {
			sx, sy = 0, 0
		}
		snakes = append(snakes, step{sx, sy, x, y})
		x, y = px, py
	}
	// snakes are in reverse order; turn the gaps between them into hunks
	var hunks []Hunk
	ax, by := 0, 0
	for i := len(snakes) - 1; i >= 0; i-- {
		s := snakes[i]
		if s.x0 > ax || s.y0 > by {
			hunks = append(hunks, Hunk{shift + ax, shift + s.x0, shift + by, shift + s.y0})
		}
		ax, by = s.x1, s.y1
	}
	if ax < n || by < m {
		hunks = append(hunks, Hunk{shift + ax, shift + n, shift + by, shift + m})
	}
	return hunks
}
