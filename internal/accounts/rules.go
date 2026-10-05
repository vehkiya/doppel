package accounts

import (
	"sort"

	"github.com/vehkiya/doppel/internal/paths"
)

// Rule is one folder rule: repos inside Folder use the account ID.
type Rule struct{ Folder, ID string }

// FolderRules lists every folder rule in the order the index gives them to
// Git: from broad to specific (by depth, then alphabetically), because Git
// lets the last match win.
func FolderRules(env *paths.Env, list []*Account) []Rule {
	var rules []Rule
	for _, a := range list {
		for _, f := range a.Folders {
			rules = append(rules, Rule{Folder: f, ID: a.ID})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		di, dj := env.FolderDepth(rules[i].Folder), env.FolderDepth(rules[j].Folder)
		if di != dj {
			return di < dj
		}
		return rules[i].Folder < rules[j].Folder
	})
	return rules
}

// MatchFolder returns the rule Git applies to a repo at path (a real path):
// the last of FolderRules that holds it, as Git picks it. ok is false when
// no rule does, so the default account applies.
func MatchFolder(env *paths.Env, list []*Account, path string) (rule Rule, ok bool) {
	for _, r := range FolderRules(env, list) {
		if env.FolderContains(r.Folder, path) {
			rule, ok = r, true
		}
	}
	return rule, ok
}
