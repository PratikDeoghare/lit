package lit

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var labels = map[string]string{
	"lit":      "made in the .lit; the generated file does not have it",
	"gen":      "made in the generated file; the .lit does not have it",
	"same":     "the same change on both sides",
	"conflict": "CONFLICT: both sides changed these lines differently",
}

const interactiveHelp = `
    y  apply it to the other side         n  leave it pending          r  revert it where it was made
    e  edit the text, then apply to both  a  apply all remaining in this file     A  apply all remaining
    d  done: leave everything else pending                 p  previous      q  quit, change nothing
For a conflict:
    l  take the .lit's version   g  take the file's version   b  both (.lit's then file's)
    e  edit   r  revert both to the base   n  leave pending   d / p / q as above
`

// syncUI carries the streams and the colour decision of one sync command.
type syncUI struct {
	out, err io.Writer
	in       *bufio.Reader
	color    bool
}

func (u *syncUI) paint(s, code string) string {
	if !u.color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (u *syncUI) printf(f string, a ...any) { fmt.Fprintf(u.out, f, a...) }
func (u *syncUI) println(a ...any)          { fmt.Fprintln(u.out, a...) }

func where(r Region) string {
	if r.E > r.S {
		return fmt.Sprintf("base lines %d-%d", r.S+1, r.E)
	}
	return fmt.Sprintf("inserted after base line %d", r.S)
}

func (u *syncUI) showRegion(fs *FileState, i int) {
	const ctx = 3
	r := fs.Regions[i]
	kind := map[string]string{"lit": "36", "gen": "33", "same": "32", "conflict": "31;1"}[r.Kind]
	u.println(u.paint(fmt.Sprintf("\n== %s  region %d/%d  (%s)  %s", fs.Name, i+1, len(fs.Regions), where(r), labels[r.Kind]), kind))
	for n := max(0, r.S-ctx); n < r.S; n++ {
		u.printf("   %4d | %s\n", n+1, fs.Base[n])
	}
	nothing := func(l []string) string {
		if len(l) == 0 {
			return u.paint("(nothing)", "2")
		}
		return strings.Join(l, "\n")
	}
	if r.Kind == "conflict" {
		u.println(u.paint("<<<<<<< .lit side", "36"))
		u.println(nothing(r.Lit))
		u.println("||||||| base")
		u.println(nothing(r.Base))
		u.println(u.paint("======= generated file", "33"))
		u.println(nothing(r.Gen))
		u.println(">>>>>>>")
	} else {
		nw := r.Gen
		if r.Kind == "lit" || r.Kind == "same" {
			nw = r.Lit
		}
		for _, l := range r.Base {
			u.println(u.paint(" -     | "+l, "31"))
		}
		for _, l := range nw {
			u.println(u.paint(" +     | "+l, "32"))
		}
	}
	for n := r.E; n < min(len(fs.Base), r.E+ctx); n++ {
		u.printf("   %4d | %s\n", n+1, fs.Base[n])
	}
}

func (u *syncUI) litDiff(an *Analysis, newText string) string {
	changed, err := an.Src.SplitBack(newText)
	if err != nil {
		return err.Error()
	}
	var names []string
	for n := range changed {
		names = append(names, n)
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		parts = append(parts, strings.Join(UnifiedDiff(strings.Split(an.Src.Texts[n], "\n"), strings.Split(changed[n], "\n"), "a/"+n, "b/"+n, 1), "\n"))
	}
	return strings.Join(parts, "\n")
}

func (u *syncUI) describe(an *Analysis, out *Outcome, decisions map[RegionKey]Decision) {
	if out.LitChanged {
		u.println(u.paint("\nthe .lit would change:", "1"))
		u.println(u.litDiff(an, out.NewLit))
	} else {
		u.println("\nthe .lit would not change")
	}
	var rewrite []string
	for _, n := range an.Names {
		fs := an.Files[n]
		cur, _ := ReadDisk(filepath.Join(an.Outdir, n))
		if fs.Missing || !equalLines(ToLines(cur), ToLines(out.GenText[n])) {
			rewrite = append(rewrite, n)
		}
	}
	if len(rewrite) == 0 {
		u.println("generated files rewritten: none")
	} else {
		u.println("generated files rewritten: " + strings.Join(rewrite, ", "))
	}
	left, unresolved := 0, 0
	for _, d := range decisions {
		if d.Act == Leave {
			left++
		}
	}
	for _, n := range an.Names {
		for i, r := range an.Files[n].Regions {
			if _, ok := decisions[RegionKey{n, i}]; r.Kind == "conflict" && !ok {
				unresolved++
			}
		}
	}
	if left+unresolved > 0 {
		u.printf("left pending: %d region(s)\n", left+unresolved)
	}
	for _, n := range out.Notes {
		u.println("note: " + n)
	}
}

func (u *syncUI) reportProblems(out *Outcome) {
	for _, e := range out.Errors {
		u.println(u.paint("cannot sync: "+e, "31"))
	}
	var names []string
	for n := range out.Ripple {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w := out.Ripple[n]
		u.println(u.paint(fmt.Sprintf("side effect in %s: the patched .lit regenerates it differently from what was chosen (a chunk used in several places)", n), "33"))
		d := UnifiedDiff(w[0], w[1], "chosen", "regenerated from the .lit", 0)
		for i, l := range d {
			if i >= 12 {
				break
			}
			u.println("    " + l)
		}
	}
}

var errQuit = fmt.Errorf("quit")

func (u *syncUI) ask(prompt, valid string) (byte, error) {
	for {
		fmt.Fprint(u.out, prompt)
		line, err := u.in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" && strings.IndexByte(valid, line[0]) >= 0 {
			return line[0], nil
		}
		if err != nil {
			return 0, errQuit
		}
		u.println("  choose one of: " + strings.Join(strings.Split(valid, ""), " ") + "   (? for help)")
	}
}

func editText(lines []string) ([]string, error) {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	f, err := os.CreateTemp("", "lit-*.txt")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	text := strings.Join(lines, "\n")
	if len(lines) > 0 {
		text += "\n"
	}
	f.WriteString(text)
	f.Close()
	fields := strings.Fields(editor)
	cmd := exec.Command(fields[0], append(fields[1:], f.Name())...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Run()
	t, err := ReadDisk(f.Name())
	return ToLines(t), err
}

func (u *syncUI) pick(an *Analysis, names []string, yes, force bool) int {
	type rk = RegionKey
	var todo []rk
	for _, n := range names {
		for i := range an.Files[n].Regions {
			todo = append(todo, rk{n, i})
		}
	}
	if len(todo) == 0 {
		u.println("nothing to choose: both sides agree.")
		return 0
	}
	decisions := map[rk]Decision{}
	auto := ""
	for k := 0; k < len(todo); {
		key := todo[k]
		fs := an.Files[key.File]
		r := fs.Regions[key.I]
		if auto != "" && r.Kind != "conflict" && (auto == "ALL" || auto == key.File) {
			decisions[key] = Decision{Act: Apply}
			k++
			continue
		}
		u.showRegion(fs, key.I)
		var keys, question string
		if r.Kind == "conflict" {
			keys = "lgbernpdq?"
			question = "[l]it's  [g]en's  [b]oth  [e]dit  [r]evert  [n]ext/leave  [p]rev  [d]one  [q]uit  [?] > "
		} else {
			keys = "ynreaApdq?"
			to := "both"
			switch r.Kind {
			case "lit":
				to = "the generated file"
			case "gen":
				to = "the .lit"
			}
			question = fmt.Sprintf("apply to %s? [y]es [n]o/leave [r]evert [e]dit [a]ll in file [A]ll [p]rev [d]one [q]uit [?] > ", to)
		}
		c, err := u.ask(question, keys)
		if err != nil || c == 'q' {
			u.println("\nquit: nothing changed.")
			return 1
		}
		switch {
		case c == '?':
			fmt.Fprint(u.out, interactiveHelp)
			continue
		case c == 'p':
			k = max(0, k-1)
			continue
		case c == 'd':
			for _, t := range todo[k:] {
				if _, ok := decisions[t]; !ok {
					decisions[t] = Decision{Act: Leave}
				}
			}
			k = len(todo)
			continue
		case c == 'y' && r.Kind != "conflict":
			decisions[key] = Decision{Act: Apply}
		case c == 'n':
			decisions[key] = Decision{Act: Leave}
		case c == 'r':
			decisions[key] = Decision{Act: Revert}
		case c == 'l':
			decisions[key] = Decision{Act: TakeLit}
		case c == 'g':
			decisions[key] = Decision{Act: TakeGen}
		case c == 'b':
			decisions[key] = Decision{Act: Both}
		case c == 'e':
			start := r.Gen
			if r.Kind == "lit" || r.Kind == "same" || r.Kind == "conflict" {
				start = r.Lit
			}
			text, _ := editText(start)
			decisions[key] = Decision{Act: Custom, Custom: text}
		case c == 'a':
			auto = key.File
			decisions[key] = Decision{Act: Apply}
		case c == 'A':
			auto = "ALL"
			decisions[key] = Decision{Act: Apply}
		}
		k++
	}
	out, err := Prepare(an, decisions)
	if err != nil {
		fmt.Fprintln(u.err, "sync: "+err.Error())
		return 2
	}
	return u.finish(an, out, decisions, yes, force, true, false)
}

func (u *syncUI) finish(an *Analysis, out *Outcome, decisions map[RegionKey]Decision, yes, force, interactive, dry bool) int {
	u.describe(an, out, decisions)
	u.reportProblems(out)
	if len(out.Errors) > 0 {
		return 1
	}
	if len(out.Ripple) > 0 && !force {
		if !interactive {
			u.println("\nnothing written. Make the same edit in every copy of the chunk, or pass --force to let the .lit's version win.")
			return 1
		}
		if c, err := u.ask("\nAccept these side effects (the .lit wins)? [y/n] > ", "yn"); err != nil || c != 'y' {
			u.println("nothing written.")
			return 1
		}
	}
	if dry {
		u.println("\n(dry run: nothing written)")
		return 0
	}
	if interactive && !yes {
		if c, err := u.ask("\nWrite these changes? [y/n] > ", "yn"); err != nil || c != 'y' {
			u.println("nothing written.")
			return 1
		}
	}
	written, err := Commit(an, out, "sync")
	if err != nil {
		fmt.Fprintln(u.err, "sync: "+err.Error())
		return 2
	}
	state := "unchanged"
	if out.LitChanged {
		state = "updated"
	}
	w := "no generated files"
	if len(written) > 0 {
		w = strings.Join(written, ", ")
	}
	u.printf("\nsynced. .lit %s; wrote: %s. Previous versions are kept (see `log`).\n", state, w)
	return 0
}

func mtime(path string) float64 {
	if fi, err := os.Stat(path); err == nil {
		return float64(fi.ModTime().UnixNano()) / 1e9
	}
	return 0
}

func newer(an *Analysis, name string) string {
	var tLit float64
	for _, n := range an.Src.Order {
		t, ok := an.Store.LastEdit("lit", n)
		if !ok {
			t = mtime(filepath.Join(an.Src.Base, n))
		}
		tLit = max(tLit, t)
	}
	tGen, ok := an.Store.LastEdit("gen", name)
	if !ok {
		tGen = mtime(filepath.Join(an.Outdir, name))
	}
	if tLit >= tGen {
		return "lit"
	}
	return "gen"
}

func (u *syncUI) merge(an *Analysis, prefer string, dry, force bool) int {
	decisions := map[RegionKey]Decision{}
	undecided := 0
	for _, n := range an.Names {
		for i, r := range an.Files[n].Regions {
			if r.Kind != "conflict" {
				continue
			}
			side := prefer
			if prefer == "newer" {
				side = newer(an, n)
			}
			switch side {
			case "lit":
				decisions[RegionKey{n, i}] = Decision{Act: TakeLit}
			case "gen":
				decisions[RegionKey{n, i}] = Decision{Act: TakeGen}
			default:
				undecided++
			}
		}
	}
	any := false
	for _, fs := range an.Files {
		any = any || len(fs.Regions) > 0 || fs.Missing
	}
	if !any {
		u.println("nothing to merge: both sides agree.")
		return 0
	}
	out, err := Prepare(an, decisions)
	if err != nil {
		fmt.Fprintln(u.err, "sync: "+err.Error())
		return 2
	}
	rc := u.finish(an, out, decisions, true, force, false, dry)
	if rc == 0 && undecided > 0 {
		u.println(u.paint(fmt.Sprintf("%d conflict(s) left pending; resolve with `pick` or `merge --prefer ...`", undecided), "33"))
		return 1
	}
	return rc
}

func (u *syncUI) status(an *Analysis, check bool) int {
	pending := 0
	if an.LitChanged() {
		u.println("the .lit has changed since the last sync")
	}
	for _, n := range an.Names {
		fs := an.Files[n]
		if len(fs.Regions) == 0 && !fs.Missing {
			u.printf("  %-28s in sync\n", n)
			continue
		}
		c := map[string]int{}
		for _, r := range fs.Regions {
			c[r.Kind]++
		}
		pending += len(fs.Regions)
		if fs.Missing {
			pending++
		}
		var bits []string
		if c["lit"] > 0 {
			bits = append(bits, fmt.Sprintf("%d from the .lit", c["lit"]))
		}
		if c["gen"] > 0 {
			bits = append(bits, fmt.Sprintf("%d edited in the file", c["gen"]))
		}
		if c["same"] > 0 {
			bits = append(bits, fmt.Sprintf("%d identical", c["same"]))
		}
		if c["conflict"] > 0 {
			bits = append(bits, u.paint(fmt.Sprintf("%d CONFLICT", c["conflict"]), "31"))
		}
		if fs.Missing {
			bits = append(bits, "file missing")
		}
		u.printf("  %-28s %s\n", n, strings.Join(bits, ", "))
	}
	if pending > 0 {
		u.printf("\n%d thing(s) to reconcile: lit sync %s merge   or   ... pick\n", pending, an.Source)
	} else {
		u.println("\neverything is in sync.")
	}
	if check && pending > 0 {
		return 1
	}
	return 0
}

func (u *syncUI) showLog(st *Store, n int, path string) {
	var evs []Event
	for _, e := range st.Events() {
		if path == "" || e.Path == path {
			evs = append(evs, e)
		}
	}
	if len(evs) > n {
		evs = evs[len(evs)-n:]
	}
	for _, e := range evs {
		when := time.Unix(int64(e.T), 0).Format("2006-01-02 15:04:05")
		extra := e.Note
		if extra == "" {
			extra = strings.Join(e.Files, ", ")
		}
		sh := e.Sha
		if sh == "" {
			sh = "-"
		}
		u.printf("%s  %-9s %-4s %-28s %s  %s\n", when, e.Kind, e.Side, e.Path, sh[:min(8, len(sh))], extra)
	}
	if len(evs) == 0 {
		u.println("(the journal is empty)")
	}
}

func (u *syncUI) restore(an *Analysis, path, shaPrefix string, toStdout, listOnly bool) int {
	st := an.Store
	var versions []Event
	for _, e := range st.Events() {
		if e.Path == path && e.Sha != "" {
			versions = append(versions, e)
		}
	}
	if listOnly || shaPrefix == "" {
		for _, e := range versions {
			u.printf("%s  %-9s %s  %s\n", time.Unix(int64(e.T), 0).Format("15:04:05"), e.Kind, e.Sha[:8], e.Note)
		}
		if len(versions) == 0 {
			u.printf("no recorded versions of %s\n", path)
		}
		return 0
	}
	h, text, err := st.ReadSnapshot(shaPrefix)
	if err != nil {
		fmt.Fprintln(u.err, "sync: "+err.Error())
		return 2
	}
	if toStdout {
		io.WriteString(u.out, text)
		return 0
	}
	_, isLit := an.Src.Texts[path]
	side, target := "gen", filepath.Join(an.Outdir, path)
	if isLit {
		side, target = "lit", filepath.Join(an.Src.Base, path)
	}
	if cur, err := ReadDisk(target); err == nil {
		st.Log("backup", side, path, &cur, map[string]any{"note": "before restore"})
	}
	if err := writeFile(target, text); err != nil {
		fmt.Fprintln(u.err, "sync: "+err.Error())
		return 2
	}
	st.Log("restore", side, path, &text, map[string]any{"note": "restored " + h[:8]})
	u.printf("restored %s to %s; it now counts as an edit, run `status` or `merge`\n", path, h[:8])
	return 0
}

func (u *syncUI) adopt(source, outdir, baseLit string, genIsBase bool) error {
	st := NewStore(source, outdir)
	cur, err := ReadSource(source)
	if err != nil {
		return err
	}
	switch {
	case baseLit != "":
		old, err := ReadSource(baseLit)
		if err != nil {
			return err
		}
		files, _, err := TangleText(old.Text)
		if err != nil {
			return err
		}
		if err := st.SetBase(files, old.Text, "adopt", old.Texts); err != nil {
			return err
		}
	case genIsBase:
		tangled, _, err := TangleText(cur.Text)
		if err != nil {
			return err
		}
		files := map[string]string{}
		for n := range tangled {
			files[n], _ = ReadDisk(filepath.Join(outdir, n))
		}
		if err := st.SetBase(files, cur.Text, "adopt", cur.Texts); err != nil {
			return err
		}
	default:
		return fmt.Errorf("say what the base is: --base-lit OLD.lit or --gen-is-base")
	}
	u.println("base recorded; run `status`")
	return nil
}

var syncSpec = Spec{
	Opts: map[string]bool{"outdir": true, "check": false, "prefer": true, "dry-run": false, "force": false, "yes": false,
		"n": true, "path": true, "sha": true, "list": false, "stdout": false, "base-lit": true, "gen-is-base": false},
	Short: map[string]string{"o": "outdir"},
}

const syncUsage = "usage: lit sync SOURCE.lit status|diff|merge|pick|log|restore|adopt [-o OUTDIR] ..."

// Sync reconciles a .lit with the files tangled from it, in both directions.
func Sync(argv []string, io IO) int {
	a, err := ParseArgs(argv, syncSpec)
	if err != nil || len(a.Pos) < 2 {
		fmt.Fprintln(io.Err, syncUsage)
		return 2
	}
	source, cmd, rest := a.Pos[0], a.Pos[1], a.Pos[2:]
	outdir := a.Get("outdir", ".")
	u := &syncUI{out: io.Out, err: io.Err, in: bufio.NewReader(io.In), color: io.Out == os.Stdout && IsTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""}
	fail := func(err error) int { fmt.Fprintln(io.Err, "sync: "+err.Error()); return 2 }
	switch cmd {
	case "adopt":
		if err := u.adopt(source, outdir, a.Get("base-lit", ""), a.Has("gen-is-base")); err != nil {
			return fail(err)
		}
		return 0
	case "log":
		n := 30
		if v := a.Get("n", ""); v != "" {
			n, _ = strconv.Atoi(v)
		}
		u.showLog(NewStore(source, outdir), n, a.Get("path", ""))
		return 0
	case "status", "diff", "merge", "pick", "restore":
	default:
		fmt.Fprintln(io.Err, syncUsage)
		return 2
	}
	an, err := NewAnalysis(source, outdir)
	if err != nil {
		return fail(err)
	}
	switch cmd {
	case "status":
		return u.status(an, a.Has("check"))
	case "restore":
		if len(rest) != 1 {
			fmt.Fprintln(io.Err, "usage: lit sync SOURCE.lit restore PATH [--list | --sha S | --stdout]")
			return 2
		}
		return u.restore(an, rest[0], a.Get("sha", ""), a.Has("stdout"), a.Has("list"))
	}
	for _, f := range rest {
		if _, ok := an.Files[f]; !ok {
			return fail(fmt.Errorf("not generated from %s: %s", source, f))
		}
	}
	var names []string
	cands := an.Names
	if (cmd == "diff" || cmd == "pick") && len(rest) > 0 {
		cands = rest
	}
	for _, f := range cands {
		if len(an.Files[f].Regions) > 0 {
			names = append(names, f)
		}
	}
	switch cmd {
	case "diff":
		for _, n := range names {
			for i := range an.Files[n].Regions {
				u.showRegion(an.Files[n], i)
			}
		}
		if len(names) == 0 {
			u.println("both sides agree.")
		}
		return 0
	case "merge":
		return u.merge(an, a.Get("prefer", ""), a.Has("dry-run"), a.Has("force"))
	}
	return u.pick(an, names, a.Has("yes"), a.Has("force"))
}
