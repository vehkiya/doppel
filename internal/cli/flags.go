package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/vehkiya/doppel/internal/ui"
)

// stringList is a repeatable flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("doppel "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags parses flags that may come before, between or after the
// positional arguments, and returns the positional ones.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

// flagWasSet reports whether a flag appeared on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// writeFlags are shared by every command that changes files.
type writeFlags struct {
	dryRun bool
	yes    bool
}

func (w *writeFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&w.dryRun, "dry-run", false, "show the changes without writing them")
	fs.BoolVar(&w.yes, "yes", false, "answer yes to confirmations")
}

// assumeYes is true when confirmations needn't be asked: with --yes, or
// with --dry-run, which changes nothing.
func (w *writeFlags) assumeYes() bool { return w.yes || w.dryRun }

// accountFlags are the account fields shared by add and edit.
type accountFlags struct {
	name, email, githubUser string
	hosts, folders          stringList
	makeDefault             bool
}

func (f *accountFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.name, "name", "", "commit author name")
	fs.StringVar(&f.email, "email", "", "commit author email")
	fs.StringVar(&f.githubUser, "github-user", "", "GitHub username")
	fs.Var(&f.hosts, "host", "Git host (repeatable)")
	fs.Var(&f.folders, "folder", "folder the account applies to (repeatable)")
	fs.BoolVar(&f.makeDefault, "default", false, "make this the default account")
}

// parseCommand parses a command's flags, printing its usage on errors and
// for --help. ok is false when the command should stop with code.
//
// Every command calls it before doing anything else, so shell completion
// can learn the command's flags (collectFlags) without running it.
func (a *app) parseCommand(fs *flag.FlagSet, args []string, usage string) (positional []string, code int, ok bool) {
	if a.collectFlags != nil {
		a.collectFlags(fs)
		return nil, 0, false
	}
	positional, err := parseFlags(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		a.printf("Usage: %s\n", usage)
		return nil, 0, false
	}
	if err != nil {
		_, _ = fmt.Fprintln(a.stderr, ui.Error.Render("✗")+" "+err.Error())
		return nil, a.usageError(usage), false
	}
	return positional, 0, true
}
