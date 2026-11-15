package lit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pratikdeoghare/brashtag-literate/internal/skill"
)

// Version is set at build time with -ldflags "-X .../internal/lit.Version=1.2.3".
var Version = "dev"

const (
	skillName = "brashtag-literate"
	marker    = ".installed-by-lit"
)

func skillsRoot(dest string) string {
	if dest == "" {
		home, _ := os.UserHomeDir()
		dest = filepath.Join(home, ".claude", "skills")
	}
	if strings.HasPrefix(dest, "~") {
		home, _ := os.UserHomeDir()
		dest = filepath.Join(home, dest[1:])
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return dest
	}
	return abs
}

func skillTarget(dest string) string { return filepath.Join(skillsRoot(dest), skillName) }

func installedByUs(path string) bool {
	fi, err := os.Stat(filepath.Join(path, marker))
	return err == nil && fi.Mode().IsRegular()
}

func lexists(path string) bool { _, err := os.Lstat(path); return err == nil }

func removeAny(path string) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return os.Remove(path)
	}
	return os.RemoveAll(path)
}

// InstallSkill copies the embedded skill to <dest>/brashtag-literate.
func InstallSkill(dest string, force bool, io IO) (string, error) {
	where := skillTarget(dest)
	if lexists(where) {
		if !installedByUs(where) && !force {
			return "", fmt.Errorf("%s exists and was not put there by lit; pass --force to replace it", where)
		}
		if err := removeAny(where); err != nil {
			return "", err
		}
	}
	err := fs.WalkDir(skill.FS, skill.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, skill.Root), "/")
		out := filepath.Join(where, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := skill.FS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(where, marker), []byte(Version+"\n"), 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(io.Out, "installed the skill %s %s in %s\n", skillName, Version, where)
	return where, nil
}

func UninstallSkill(dest string, force bool, io IO) (bool, error) {
	where := skillTarget(dest)
	if !lexists(where) {
		fmt.Fprintf(io.Out, "no skill at %s; nothing to remove\n", where)
		return false, nil
	}
	if !installedByUs(where) && !force {
		return false, fmt.Errorf("%s was not put there by lit, so it is left alone; pass --force to remove it", where)
	}
	if err := removeAny(where); err != nil {
		return false, err
	}
	fmt.Fprintf(io.Out, "removed %s\n", where)
	return true, nil
}

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

var installSpec = Spec{
	Opts:  map[string]bool{"dest": true, "force": false, "yes": false, "skill-only": false},
	Short: map[string]string{"y": "yes"},
}

func installCmd(which string) func([]string, IO) int {
	return func(argv []string, io IO) int {
		a, err := ParseArgs(argv, installSpec)
		if err != nil || len(a.Pos) != 0 {
			fmt.Fprintf(io.Err, "usage: lit %s [--dest DIR] [--force]\n", which)
			return 2
		}
		fail := func(err error) int { fmt.Fprintln(io.Err, "lit: "+err.Error()); return 2 }
		switch which {
		case "install-skill":
			if _, err := InstallSkill(a.Get("dest", ""), a.Has("force"), io); err != nil {
				return fail(err)
			}
		case "uninstall-skill":
			if _, err := UninstallSkill(a.Get("dest", ""), a.Has("force"), io); err != nil {
				return fail(err)
			}
		case "uninstall":
			if _, err := UninstallSkill(a.Get("dest", ""), false, io); err != nil {
				return fail(err)
			}
			if a.Has("skill-only") {
				return 0
			}
			exe := exePath()
			if !a.Has("yes") {
				if !io.IsTTY {
					return fail(fmt.Errorf("removing the program needs --yes when there is no terminal to ask on"))
				}
				fmt.Fprintf(io.Out, "remove the program %s as well? [y/N] ", exe)
				var ans string
				fmt.Fscanln(io.In, &ans)
				if strings.ToLower(strings.TrimSpace(ans)) != "y" {
					fmt.Fprintln(io.Out, "program kept; the skill is removed")
					return 0
				}
			}
			if runtime.GOOS == "windows" {
				fmt.Fprintf(io.Out, "delete %s yourself: a running program cannot remove itself on Windows\n", exe)
				return 0
			}
			if err := os.Remove(exe); err != nil {
				return fail(err)
			}
			fmt.Fprintf(io.Out, "removed %s\n", exe)
		}
		return 0
	}
}

// Where says what is installed and where.
func Where(argv []string, io IO) int {
	a, _ := ParseArgs(argv, installSpec)
	skillDir := skillTarget(a.Get("dest", ""))
	state := "not installed"
	switch {
	case installedByUs(skillDir):
		state = "installed"
	case lexists(skillDir):
		state = "present, but not installed by lit"
	}
	fmt.Fprintf(io.Out, "brashtag-literate %s\n  program   %s\n  skill     %s (%s)\n", Version, exePath(), skillDir, state)
	return 0
}
