package lit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// A Finding is one result of a check. Level is "error" (the program is broken), "warn" (look at it) or "ok".
type Finding struct {
	Level string
	Msg   string
}

func (f Finding) String() string { return f.Level + ": " + f.Msg }

func hasError(fs []Finding) bool {
	for _, f := range fs {
		if f.Level == "error" {
			return true
		}
	}
	return false
}

// spotOf says where a line of a generated file comes from: "file:line" of the program, or "program line N"
// when the text being checked is not on disk.
func spotOf(src *Source, text string, m *Map, file string, line int) string {
	e, ok := m.Files[file]
	org := e.Origins()
	if !ok || line < 1 || line > len(org) {
		return fmt.Sprintf("%s:%d", file, line)
	}
	o := org[line-1]
	off := m.Blocks[o.Block].Lines[o.Line][0]
	if src != nil && src.Text == text {
		return fmt.Sprintf("%s (%s)", src.Where(off), file+":"+fmt.Sprint(line))
	}
	return fmt.Sprintf("program line %d (%s:%d)", strings.Count(text[:min(off, len(text))], "\n")+1, file, line)
}

var placeholderRe = regexp.MustCompile(`(?i)\b(TODO|FIXME|XXX)\b|not implemented|unimplemented|implement me|your code here|\.\.\.\s*$|^\s*pass\s*(#.*)?$`)

// CheckProgram runs the checks that need no model: the program must parse and tangle, every generated Go file
// and JSON file must be syntactically valid, and the generated text must not still hold the marks of a gap.
// text is the program text to check (the flat text; it need not be what is on disk).
func CheckProgram(src *Source, text string) []Finding {
	ns := &Source{Root: src.Root, Base: src.Base, Texts: src.Texts, Order: src.Order, Text: text, Pieces: nil}
	if src.Text == text {
		ns = src
	}
	if _, err := ns.Parse(); err != nil {
		return []Finding{{"error", "the program no longer parses: " + err.Error()}}
	}
	files, order, err := TangleText(text)
	if err != nil {
		return []Finding{{"error", "the program does not tangle: " + err.Error()}}
	}
	m, _, err := BuildMap(ns)
	if err != nil {
		m = nil
	}
	var out []Finding
	for _, name := range order {
		body := files[name]
		report := func(line int, msg string) {
			where := name + fmt.Sprintf(":%d", line)
			if m != nil {
				where = spotOf(src, text, m, name, line)
			}
			out = append(out, Finding{"error", fmt.Sprintf("%s: %s", where, msg)})
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".go":
			fset := token.NewFileSet()
			if _, err := parser.ParseFile(fset, name, body, parser.AllErrors|parser.ParseComments); err != nil {
				if el, ok := err.(scanner.ErrorList); ok {
					for i, e := range el {
						if i == 3 {
							out = append(out, Finding{"error", fmt.Sprintf("%s: ... and %d more syntax error(s)", name, len(el)-3)})
							break
						}
						report(e.Pos.Line, "syntax error: "+e.Msg)
					}
				} else {
					out = append(out, Finding{"error", name + ": " + err.Error()})
				}
			}
		case ".json":
			var v any
			if err := json.Unmarshal([]byte(body), &v); err != nil {
				out = append(out, Finding{"error", name + ": invalid JSON: " + err.Error()})
			}
		}
	}
	return out
}

// CheckFillCode looks at the lines of one filled gap on their own.
func CheckFillCode(code []string, hint string) []Finding {
	var out []Finding
	if strings.TrimSpace(strings.Join(code, "")) == "" {
		return []Finding{{"error", "the filled code is empty"}}
	}
	for i, l := range code {
		if placeholderRe.MatchString(l) {
			out = append(out, Finding{"warn", fmt.Sprintf("line %d looks like a placeholder: %s", i+1, strings.TrimSpace(l))})
		}
		if strings.Contains(l, "\x00") {
			out = append(out, Finding{"error", "the code contains a NUL character"})
		}
	}
	if len(code) > 80 {
		out = append(out, Finding{"warn", fmt.Sprintf("%d lines for one gap; consider splitting it into named chunks with prose", len(code))})
	}
	tabs, spaces := 0, 0
	for _, l := range code {
		switch {
		case strings.HasPrefix(l, "\t"):
			tabs++
		case strings.HasPrefix(l, "  "):
			spaces++
		}
	}
	if tabs > 0 && spaces > 0 {
		out = append(out, Finding{"warn", "the code mixes tab and space indentation"})
	}
	return out
}

// RunCheck tangles the program into a scratch directory and runs a shell command there (go vet ./... say).
func RunCheck(text, cmdline string, timeout time.Duration) []Finding {
	files, _, err := TangleText(text)
	if err != nil {
		return []Finding{{"error", "does not tangle: " + err.Error()}}
	}
	dir, err := os.MkdirTemp("", "lit-check")
	if err != nil {
		return []Finding{{"error", err.Error()}}
	}
	defer os.RemoveAll(dir)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(n)), files[n]); err != nil {
			return []Finding{{"error", err.Error()}}
		}
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdline)
	} else {
		cmd = exec.Command("sh", "-c", cmdline)
	}
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return []Finding{{"error", "check command: " + err.Error()}}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(buf.String())
			if len(msg) > 1500 {
				msg = msg[:1500] + "\n..."
			}
			return []Finding{{"error", fmt.Sprintf("`%s` failed:\n%s", cmdline, msg)}}
		}
	case <-time.After(timeout):
		cmd.Process.Kill()
		return []Finding{{"error", fmt.Sprintf("`%s` timed out after %s", cmdline, timeout)}}
	}
	return []Finding{{"ok", "`" + cmdline + "` passed"}}
}
