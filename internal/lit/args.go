package lit

import (
	"fmt"
	"io"
	"strings"
)

// IO carries the three streams a command works with, so tests can run commands in memory.
type IO struct {
	In       io.Reader
	Out, Err io.Writer
	IsTTY    bool // is stdin a terminal (interactive prompts only make sense then)
}

// Spec describes the options of a command: name -> whether it takes a value. Short names map to long ones.
type Spec struct {
	Opts  map[string]bool
	Short map[string]string
}

// Args is a parsed command line. Options may come before, between or after the positional arguments.
type Args struct {
	Pos  []string
	opts map[string][]string
}

func ParseArgs(argv []string, spec Spec) (*Args, error) {
	a := &Args{opts: map[string][]string{}}
	for i := 0; i < len(argv); i++ {
		s := argv[i]
		if s == "--" {
			a.Pos = append(a.Pos, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(s, "-") || s == "-" || isNumber(s) {
			a.Pos = append(a.Pos, s)
			continue
		}
		name, val, hasVal := strings.TrimLeft(s, "-"), "", false
		if k := strings.IndexByte(name, '='); k >= 0 {
			name, val, hasVal = name[:k], name[k+1:], true
		}
		if long, ok := spec.Short[name]; ok && !strings.HasPrefix(s, "--") {
			name = long
		}
		takes, known := spec.Opts[name]
		if !known {
			return nil, fmt.Errorf("unknown option %s", s)
		}
		if takes && !hasVal {
			if i+1 >= len(argv) {
				return nil, fmt.Errorf("option %s needs a value", s)
			}
			i++
			val = argv[i]
		} else if !takes {
			val = "true"
		}
		a.opts[name] = append(a.opts[name], val)
	}
	return a, nil
}

func isNumber(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (a *Args) Has(name string) bool { return len(a.opts[name]) > 0 }

// Get returns the last value given for an option, or def.
func (a *Args) Get(name, def string) string {
	if v := a.opts[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return def
}

func (a *Args) All(name string) []string { return a.opts[name] }
