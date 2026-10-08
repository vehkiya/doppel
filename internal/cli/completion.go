package cli

import (
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
)

const completionUsage = "doppel completion zsh|bash|fish"

// completeCommand is the hidden command the completion scripts run:
// `doppel __complete <words after doppel>`, the last being the word under
// the cursor (maybe empty). It prints one candidate per line, as
// "word<TAB>description" or just "word", then one last line saying what the
// shell should do: ":words" (offer the candidates), ":files" or ":dirs"
// (complete paths itself).
const completeCommand = "__complete"

// commandSummaries lists the commands completion offers, one line each.
// Aliases (list, remove, delete, upgrade) work but aren't offered.
var commandSummaries = map[string]string{
	"ls":         "List accounts",
	"add":        "Add an account",
	"edit":       "Change an account",
	"rm":         "Delete an account (key files are kept)",
	"rename":     "Change an account's ID",
	"bind":       "Use an account for repos in these folders",
	"unbind":     "Remove folder rules",
	"default":    "Show or set the default account",
	"whoami":     "Show which account applies, and why",
	"test":       "Log in to each host and sign a test message",
	"export":     "Print and copy a public key",
	"upload":     "Add the account's keys to its GitHub user",
	"doctor":     "Check for problems",
	"update":     "Install the latest signed release",
	"uninstall":  "Take doppel out of your Git config",
	"completion": "Print a shell completion script",
	"version":    "Show the version",
	"help":       "Show help",
}

// What a word completes to.
const (
	argNone     = ""
	argAccount  = "account"
	argFolder   = "folder" // a folder on disk
	argBound    = "bound"  // a folder bound to an account
	argFile     = "file"
	argHost     = "host"
	argShell    = "shell"
	argProtocol = "protocol"
)

// positional says what a command's nth positional argument is.
func positional(cmd string, n int) string {
	switch cmd {
	case "edit", "rm", "remove", "delete", "rename", "default", "test", "export", "upload":
		if n == 0 {
			return argAccount
		}
	case "bind":
		if n == 0 {
			return argAccount
		}
		return argFolder
	case "unbind":
		return argBound
	case "whoami":
		if n == 0 {
			return argFolder
		}
	case "completion":
		if n == 0 {
			return argShell
		}
	}
	return argNone
}

// flagValues says what a flag's value is.
var flagValues = map[string]string{
	"folder":      argFolder,
	"auth-key":    argFile,
	"signing-key": argFile,
	"host":        argHost,
	"protocol":    argProtocol,
}

// cmdCompletion prints the completion script for a shell.
func (a *app) cmdCompletion(args []string) int {
	positional, code, ok := a.parseCommand(newFlagSet("completion"), args, completionUsage)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return a.usageError(completionUsage)
	}
	script, ok := completionScripts[positional[0]]
	if !ok {
		return a.fail(fmt.Errorf("doppel completes zsh, bash and fish, not %s", positional[0]))
	}
	a.printf("%s", script)
	return 0
}

// cmdComplete answers a completion script (completeCommand). It never
// fails: a shell asking gets fewer candidates, not an error.
func (a *app) cmdComplete(args []string) int {
	if len(args) == 0 {
		args = []string{""}
	}
	candidates, directive := a.complete(args[:len(args)-1], args[len(args)-1])
	for _, c := range candidates {
		a.printf("%s\n", c)
	}
	a.printf(":%s\n", directive)
	return 0
}

// complete works out what the word under the cursor can be, after the words
// before it.
func (a *app) complete(before []string, current string) ([]string, string) {
	if len(before) == 0 {
		var out []string
		for name, summary := range commandSummaries {
			out = append(out, name+"\t"+summary)
		}
		slices.Sort(out)
		return out, "words"
	}
	cmd := before[0]
	fs := a.flagsOf(cmd)
	if fs == nil {
		return nil, "words"
	}

	used := map[string]bool{}
	n := 0
	for i := 1; i < len(before); i++ {
		w := before[i]
		if !strings.HasPrefix(w, "-") || w == "-" {
			n++
			continue
		}
		// --flag, -flag, or either with =value
		name, _, hasValue := strings.Cut(strings.TrimLeft(w, "-"), "=")
		used[name] = true
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && !hasValue {
			if i == len(before)-1 {
				return a.values(flagValues[name])
			}
			i++ // its value
		}
	}

	if strings.HasPrefix(current, "-") {
		var out []string
		fs.VisitAll(func(f *flag.Flag) {
			_, repeatable := f.Value.(*stringList)
			if !used[f.Name] || repeatable {
				out = append(out, "--"+f.Name+"\t"+f.Usage)
			}
		})
		return out, "words"
	}
	return a.values(positional(cmd, n))
}

// values lists what a kind of argument can be.
func (a *app) values(kind string) ([]string, string) {
	switch kind {
	case argFolder:
		return nil, "dirs"
	case argFile:
		return nil, "files"
	case argShell:
		return []string{"zsh", "bash", "fish"}, "words"
	case argProtocol:
		return []string{"ssh", "https", "both"}, "words"
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return nil, "words"
	}
	var out []string
	switch kind {
	case argAccount:
		for _, acc := range list {
			out = append(out, acc.ID+"\t"+acc.Email)
		}
	case argBound:
		for _, acc := range list {
			for _, f := range acc.Folders {
				out = append(out, f+"\t"+acc.ID)
			}
		}
	case argHost:
		out = append(out, accounts.DefaultHost)
		for _, acc := range list {
			for _, h := range acc.Hosts {
				if !slices.Contains(out, h) {
					out = append(out, h)
				}
			}
		}
	}
	return out, "words"
}

// flagsOf returns a command's flags, as the command itself defines them,
// without running it (collectFlags). Commands that don't need Git are
// handled by run itself, so they're added here.
func (a *app) flagsOf(cmd string) *flag.FlagSet {
	handlers := a.commands()
	handlers["update"], handlers["upgrade"], handlers["completion"] = a.cmdUpdate, a.cmdUpdate, a.cmdCompletion
	handler, ok := handlers[cmd]
	if !ok {
		return nil
	}
	var fs *flag.FlagSet
	a.collectFlags = func(f *flag.FlagSet) { fs = f }
	defer func() { a.collectFlags = nil }()
	handler(nil)
	return fs
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// completionScripts are thin: each asks `doppel __complete` and either
// offers its candidates or completes paths itself, so ~ and quoting work as
// the shell's own completion does.
var completionScripts = map[string]string{
	"zsh": `#compdef doppel dop
# zsh completion for doppel. Load it with: source <(doppel completion zsh)
# or save it as _doppel in a folder on $fpath.
_doppel() {
  local -a out cands
  local directive line word desc
  out=("${(@f)$(doppel __complete "${(@)words[2,CURRENT]}" 2>/dev/null)}")
  directive=${out[-1]}
  out=("${(@)out[1,-2]}")
  case $directive in
    :files) _files ;;
    :dirs) _files -/ ;;
    *)
      for line in "${out[@]}"; do
        [[ -n $line ]] || continue
        word=${line%%$'\t'*}
        desc=
        [[ $line == *$'\t'* ]] && desc=${line#*$'\t'}
        cands+=("${word//:/\\:}${desc:+:$desc}")
      done
      (( ${#cands} )) && _describe -t values doppel cands
      ;;
  esac
}
if [[ ${zsh_eval_context[-1]} == loadautofunc ]]; then
  _doppel "$@"
else
  compdef _doppel doppel dop
fi
`,
	"bash": `# bash completion for doppel. Load it with: source <(doppel completion bash)
_doppel() {
    local cur=${COMP_WORDS[COMP_CWORD]} line directive=
    local -a cands=()
    while IFS= read -r line; do
        case $line in
            :words|:files|:dirs) directive=$line ;;
            *) cands+=("${line%%$'\t'*}") ;;
        esac
    done < <(doppel __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null)
    case $directive in
        :files|:dirs)
            # Let bash complete paths itself, so ~ works; bash 3.2 has no compopt
            if type compopt &>/dev/null; then
                if [[ $directive == :dirs ]]; then compopt -o dirnames; else compopt -o default; fi
                COMPREPLY=()
            else
                local IFS=$'\n'
                if [[ $directive == :dirs ]]; then
                    COMPREPLY=($(compgen -d -- "$cur"))
                else
                    COMPREPLY=($(compgen -f -- "$cur"))
                fi
            fi
            ;;
        *)
            # Not compgen -W, which would expand ~ and $ in the candidates
            COMPREPLY=()
            for line in "${cands[@]}"; do
                [[ $line == "$cur"* ]] && COMPREPLY+=("$line")
            done
            ;;
    esac
}
complete -F _doppel doppel dop
`,
	"fish": `# fish completion for doppel. Load it with: doppel completion fish | source
function __doppel_complete
    set -l args (commandline -opc)
    set -e args[1]
    set -l cur (commandline -ct)
    set -l out (doppel __complete $args "$cur" 2>/dev/null)
    test (count $out) -gt 0; or return
    set -l directive $out[-1]
    set -e out[-1]
    switch $directive
        case :files
            __fish_complete_path "$cur"
        case :dirs
            __fish_complete_directories "$cur" ''
        case '*'
            printf '%s\n' $out
    end
end
complete -c doppel -f -a '(__doppel_complete)'
complete -c dop -w doppel
`,
}
