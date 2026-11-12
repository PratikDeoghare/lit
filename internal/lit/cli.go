package lit

import (
	"fmt"
	"os"
)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

type command struct {
	name string
	run  func(args []string, io IO) int
}

var commands = []command{
	{"tangle", Tangle},
	{"absorb", Absorb},
	{"addr", Addr},
	{"sync", Sync},
	{"watch", Watch},
	{"weave", Weave},
	{"lint", Lint},
	{"fill", Fill},
	{"review", Review},
	{"where", Where},
	{"install-skill", installCmd("install-skill")},
	{"uninstall-skill", installCmd("uninstall-skill")},
	{"uninstall", installCmd("uninstall")},
}

const helpText = `lit: one program for literate programming in brashtag notation.

  lit tangle  PROGRAM.lit [OUTDIR]              write the files a program describes
  lit weave   PROGRAM.lit [-o DIR]              woven HTML and LaTeX
  lit lint    PROGRAM.lit [--refs]              check a document before you hand it over
  lit absorb  PROGRAM.lit [FILE ...] [-o DIR]   fold edits of generated files back into the .lit
  lit sync    PROGRAM.lit COMMAND ... [-o DIR]  reconcile the .lit and its files, both ways
  lit watch   PROGRAM.lit [-o DIR]              record changes to both sides; changes nothing
  lit addr    PROGRAM.lit ADDRESS ...           translate FILE:LINE:COL between .lit and generated files
  lit fill    PROGRAM.lit [--agent SPEC] ...    let an agent write the code at #agent-fill-in{} hints
  lit review  PROGRAM.lit [--check CMD] [--ai]  check what agents filled in; approve or reject it

  lit install-skill   [--dest DIR] [--force]    teach Claude the notation (default ~/.claude/skills)
  lit uninstall-skill [--dest DIR] [--force]    remove it again
  lit uninstall       [--dest DIR] [--yes] [--skill-only]
                                                remove the skill and then this program
  lit where                                     what is installed, and where
  lit --version

Every command prints its own usage when run with the wrong arguments.
`

// Main runs one lit command and returns the exit status.
func Main(args []string, io IO) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		w := io.Out
		if len(args) == 0 {
			w = io.Err
		}
		fmt.Fprint(w, helpText)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintln(io.Out, "lit "+Version)
		return 0
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.run(args[1:], io)
		}
	}
	fmt.Fprintf(io.Err, "lit: unknown command %q; lit --help lists them\n", args[0])
	return 2
}
