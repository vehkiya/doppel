package cli

import (
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/doctor"
	"github.com/vehkiya/doppel/internal/ui"
)

const doctorUsage = "doppel doctor [--fix]"

// cmdDoctor checks everything that could make Git use the wrong account,
// and says how to fix each problem. --fix redoes doppel's own files.
func (a *app) cmdDoctor(args []string) int {
	fs := newFlagSet("doctor")
	var fix bool
	fs.BoolVar(&fix, "fix", false, "bring doppel's files up to date")
	positional, code, ok := a.parseCommand(fs, args, doctorUsage)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		return a.usageError(doctorUsage)
	}
	if fix {
		if err := a.lockWrites(); err != nil {
			return a.fail(err)
		}
	}
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	findings := doctor.Check(doctor.Options{
		Env: a.env, Accounts: list, GitHub: a.github, Keychain: a.macKeychain(), Fix: fix, Cwd: a.cwd,
	})
	a.printFindings(findings)
	if doctor.Count(findings, doctor.Problem) > 0 {
		return 1
	}
	return 0
}

// printFindings shows doctor's findings under a heading for each area, each
// with its fix below it, then counts the problems and warnings.
func (a *app) printFindings(findings []doctor.Finding) {
	marks := map[doctor.Severity]string{
		doctor.OK:      ui.OK.Render("✓"),
		doctor.Note:    ui.Dim.Render("–"),
		doctor.Warning: ui.Warn.Render("⚠"),
		doctor.Problem: ui.Error.Render("✗"),
	}
	area := ""
	for _, f := range findings {
		if f.Area != area {
			area = f.Area
			a.printf("\n%s\n", ui.Title.Render(area))
		}
		a.printf("  %s %s\n", marks[f.Severity], f.Message)
		if f.Fix != "" {
			a.printf("    %s\n", ui.Dim.Render("↳ "+f.Fix))
		}
	}

	a.printf("\n")
	problems, warnings := doctor.Count(findings, doctor.Problem), doctor.Count(findings, doctor.Warning)
	if problems == 0 && warnings == 0 {
		a.successf("No problems found")
		return
	}
	a.printf("%s, %s\n", doctor.Plural(problems, "problem"), doctor.Plural(warnings, "warning"))
}
