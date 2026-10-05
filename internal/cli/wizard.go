package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/ui"
)

// Choices in the wizard's key steps that aren't key paths.
const (
	keyKeep     = "keep"
	keyGenerate = "generate"
	keyPathEtc  = "path"
	keyNone     = "none"
	keyWithAuth = "auth"
)

// wizardResult is what the account wizard decided, ready to save.
type wizardResult struct {
	keys        keyChanges
	folders     []string // as typed; bindFolders normalizes them
	makeDefault bool
}

// answers holds the wizard's fields while it runs. Later steps and the
// review read them, so they follow earlier answers even after going back.
type answers struct {
	ID, Name, Email      string
	Hosts, GitHubUser    string
	Folders              string
	Default              bool
	Auth, AuthPath       string // a choice above, or a discovered key's path
	Signing, SigningPath string
	Sign                 []string // "commits", "tags"
	Save                 bool
}

// step is one page of the wizard. hide skips it, for example the GitHub
// username when no host is GitHub. The page is built when it's needed, so
// accessible prompts, asked one at a time, see the answers before it.
type step struct {
	group func() *huh.Group
	hide  func() bool
}

// formKeyMap is Huh's keymap with Esc as well as Ctrl+C cancelling. Select
// filtering is off, as Esc would otherwise also clear a filter.
func formKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	km.Select.Filter.SetEnabled(false)
	km.MultiSelect.Filter.SetEnabled(false)
	return km
}

// runSteps runs the wizard's pages as one form, so Shift+Tab goes back to
// any earlier page and Esc cancels from any of them. Accessible prompts
// can't go back, and Huh's accessible mode ignores hidden pages, so there
// each visible page is asked in turn instead.
func (a *app) runSteps(steps []step) error {
	if a.accessible {
		for _, st := range steps {
			if st.hide != nil && st.hide() {
				continue
			}
			if err := a.runForm(huh.NewForm(st.group())); err != nil {
				return err
			}
		}
		return nil
	}
	return a.runForm(wizardForm(steps))
}

// wizardForm puts the wizard's pages into one form.
func wizardForm(steps []step) *huh.Form {
	groups := make([]*huh.Group, len(steps))
	for i, st := range steps {
		groups[i] = st.group()
		if st.hide != nil {
			groups[i] = groups[i].WithHideFunc(st.hide)
		}
	}
	return huh.NewForm(groups...).WithKeyMap(formKeyMap())
}

// accountWizard walks through an account's settings, starting from acc: an
// account being edited, or a new one (maybe prefilled from the global Git
// config). Only when the user saves does it change acc's fields; it returns
// the changes that need more than a field: keys, folders and the default.
func (a *app) accountWizard(list []*accounts.Account, acc *accounts.Account, isNew bool) (wizardResult, error) {
	title := "Edit account " + acc.ID
	if isNew {
		title = "New account"
	}
	a.printf("\n%s %s\n%s\n", ui.BadgeAccent.Render(" DOPPEL "), ui.Title.Render(title),
		ui.Dim.Render("A Git identity, its SSH keys, and the folders where it applies."))
	if !a.accessible {
		a.printf("%s\n", ui.Dim.Render("Shift+Tab goes back · Esc cancels without saving"))
	}
	a.printf("\n")

	ans, steps := a.wizardSteps(list, acc, isNew)
	if err := a.runSteps(steps); err != nil {
		return wizardResult{}, err
	}
	if !ans.Save {
		return wizardResult{}, errCancelled
	}
	return a.applyAnswers(ans, acc), nil
}

// wizardSteps builds the wizard's pages around answers prefilled from acc.
func (a *app) wizardSteps(list []*accounts.Account, acc *accounts.Account, isNew bool) (*answers, []step) {
	ans := &answers{
		ID: acc.ID, Name: acc.Name, Email: acc.Email,
		Hosts: strings.Join(acc.Hosts, ", "), GitHubUser: acc.GitHubUser,
		Folders: strings.Join(acc.Folders, ", "),
		Save:    true,
	}
	if ans.Hosts == "" {
		ans.Hosts = accounts.DefaultHost
	}
	others := len(list)
	if !isNew {
		others--
	}
	ans.Default = acc.Default || others == 0

	// Keep the current keys; a new account gets a key of its own and signs with it.
	switch {
	case acc.AuthKey != "":
		ans.Auth = keyKeep
	case isNew:
		ans.Auth = keyGenerate
	default:
		ans.Auth = keyNone
	}
	switch {
	case acc.SigningKey != "":
		ans.Signing = keyKeep
	case isNew && ans.Auth != keyNone:
		ans.Signing = keyWithAuth
	default:
		ans.Signing = keyNone
	}
	if acc.SignCommits || acc.SigningKey == "" {
		ans.Sign = append(ans.Sign, "commits")
	}
	if acc.SignTags || acc.SigningKey == "" {
		ans.Sign = append(ans.Sign, "tags")
	}

	sshDir := filepath.Join(a.env.Home, ".ssh")
	discovered := keys.Discover(sshDir)
	githubHost := map[string]bool{} // isGitHub may ask gh, so remember its answers
	anyGitHub := func() bool {
		for _, h := range splitList(ans.Hosts) {
			if _, seen := githubHost[h]; !seen {
				githubHost[h] = isGitHub(h)
			}
			if githubHost[h] {
				return true
			}
		}
		return false
	}
	prevID, prevName, prevEmail := acc.ID, acc.Name, acc.Email

	var identity []huh.Field
	if isNew {
		identity = append(identity, huh.NewInput().Key("id").Title("Account ID").
			Description("A short name, like work or personal").
			Value(&ans.ID).
			Validate(keepIfEmpty(prevID, func(id string) error {
				if err := accounts.ValidateID(id); err != nil {
					return err
				}
				if accounts.Find(list, id) != nil {
					return fmt.Errorf("account %s already exists", id)
				}
				return nil
			})))
	}
	identity = append(identity,
		huh.NewInput().Key("name").Title("Name").Description("The author name on your commits").Value(&ans.Name).
			Validate(keepIfEmpty(prevName, func(s string) error {
				if s == "" {
					return errors.New("a name is required")
				}
				return nil
			})),
		huh.NewInput().Key("email").Title("Email").Description("The author email on your commits").Value(&ans.Email).
			Validate(keepIfEmpty(prevEmail, accounts.ValidateEmail)),
	)

	where := []huh.Field{
		huh.NewInput().Key("folders").Title("Folders").
			Description("Repos inside these folders use this account. Separate them with commas; leave empty for none").
			Placeholder("~/projects/work").
			Value(&ans.Folders).Validate(func(s string) error {
			for _, f := range splitList(s) {
				if _, _, err := a.env.NormalizeFolder(f, a.cwd); err != nil {
					return err
				}
			}
			return nil
		}),
	}
	if others > 0 {
		where = append(where, huh.NewConfirm().Key("default").Title("Make it the default account?").
			Description("The default account is used for repos outside every folder").
			Affirmative("Yes").Negative("No").Value(&ans.Default))
	}

	steps := []step{
		{group: func() *huh.Group { return huh.NewGroup(identity...).Title("Identity") }},
		{group: func() *huh.Group {
			return huh.NewGroup(
				huh.NewInput().Key("hosts").Title("Git hosts").Description("Where this account pushes, separated by commas").
					Value(&ans.Hosts).Validate(func(s string) error {
					for _, h := range splitList(s) {
						if err := accounts.ValidateHost(h); err != nil {
							return err
						}
					}
					return nil
				}),
			).Title("Hosts")
		}},
		{group: func() *huh.Group {
			return huh.NewGroup(
				huh.NewInput().Key("github-user").Title("GitHub username").
					Description("Optional. doppel checks the keys log in as this user, and uploads keys to it").
					Value(&ans.GitHubUser).Validate(accounts.ValidateGitHubUser),
			).Title("Hosts")
		}, hide: func() bool { return !anyGitHub() }},
		{group: func() *huh.Group { return huh.NewGroup(where...).Title("Where it applies") }},
		{group: func() *huh.Group {
			return huh.NewGroup(
				a.liveSelect(huh.NewSelect[string]().Key("auth").Title("Auth key").
					Description("The SSH key this account fetches and pushes with. A key of its own keeps hosts from mixing up your accounts").
					Value(&ans.Auth), func() []huh.Option[string] { return a.authOptions(ans, acc, discovered, sshDir) }, ans),
			).Title("Keys")
		}},
		{group: func() *huh.Group {
			return huh.NewGroup(
				huh.NewInput().Key("auth-path").Title("Auth key file").
					Description("A private key, or a .pub whose private key lives in an agent").
					Placeholder("~/.ssh/id_ed25519").Value(&ans.AuthPath).
					Validate(a.keyPathValidator(false)),
			).Title("Keys")
		}, hide: func() bool { return ans.Auth != keyPathEtc }},
		{group: func() *huh.Group {
			return huh.NewGroup(
				a.liveSelect(huh.NewSelect[string]().Key("signing").Title("Signing").
					Description("Signed commits show as Verified on GitHub and GitLab").
					Value(&ans.Signing), func() []huh.Option[string] { return a.signingOptions(ans, acc, discovered, sshDir) }, ans),
			).Title("Keys")
		}},
		{group: func() *huh.Group {
			return huh.NewGroup(
				huh.NewInput().Key("signing-path").Title("Signing key file").
					Description("The key's .pub has to be next to it").
					Placeholder("~/.ssh/id_ed25519").Value(&ans.SigningPath).
					Validate(a.keyPathValidator(true)),
			).Title("Keys")
		}, hide: func() bool { return ans.Signing != keyPathEtc }},
		{group: func() *huh.Group {
			return huh.NewGroup(
				huh.NewMultiSelect[string]().Key("sign").Title("Sign").
					Options(huh.NewOption("Commits", "commits"), huh.NewOption("Tags", "tags")).
					Value(&ans.Sign),
			).Title("Keys")
		}, hide: func() bool { return ans.Signing == keyNone }},
		{group: func() *huh.Group {
			return huh.NewGroup(
				a.liveNote(huh.NewNote().Title("Review"), func() string { return a.review(ans, acc) }, ans),
				huh.NewConfirm().Key("save").Title("Save this account?").Affirmative("Save").Negative("Cancel").Value(&ans.Save),
			)
		}},
	}
	return ans, steps
}

// liveSelect fills a select from options, keeping it current as earlier
// answers change. Huh's accessible mode doesn't load live options, but asks
// one page at a time, so there the options at that moment are enough.
func (a *app) liveSelect(sel *huh.Select[string], options func() []huh.Option[string], binding any) *huh.Select[string] {
	if a.accessible {
		return sel.Options(options()...)
	}
	return sel.OptionsFunc(options, binding)
}

// liveNote is liveSelect for a note's text.
func (a *app) liveNote(note *huh.Note, text func() string, binding any) *huh.Note {
	if a.accessible {
		return note.Description(text())
	}
	return note.DescriptionFunc(text, binding)
}

// authOptions lists the auth key choices: keep the current key, generate
// one, a key found in ~/.ssh, another file, or none.
func (a *app) authOptions(ans *answers, acc *accounts.Account, discovered []string, sshDir string) []huh.Option[string] {
	var options []huh.Option[string]
	if acc.AuthKey != "" {
		options = append(options, huh.NewOption("Keep "+acc.AuthKey, keyKeep))
	}
	if generated := keys.DefaultPath(sshDir, cmpOr(strings.TrimSpace(ans.ID), acc.ID, "<id>"), false); !fileOrLinkExists(generated) {
		options = append(options, huh.NewOption("Generate a new key: "+a.env.Shorten(generated), keyGenerate))
	}
	for _, k := range discovered {
		if a.env.Shorten(k) != acc.AuthKey {
			options = append(options, huh.NewOption(keyLabel(a.env.Shorten(k)), k))
		}
	}
	return append(options,
		huh.NewOption("Another key file…", keyPathEtc),
		huh.NewOption("None: use ssh's own keys", keyNone))
}

// signingOptions lists the signing choices: keep the current key, sign with
// the auth key, generate a separate key, a key found in ~/.ssh, another
// file, or don't sign.
func (a *app) signingOptions(ans *answers, acc *accounts.Account, discovered []string, sshDir string) []huh.Option[string] {
	var options []huh.Option[string]
	if acc.SigningKey != "" {
		options = append(options, huh.NewOption("Keep "+acc.SigningKey, keyKeep))
	}
	if ans.Auth != keyNone {
		options = append(options, huh.NewOption("Sign with the auth key", keyWithAuth))
	}
	if generated := keys.DefaultPath(sshDir, cmpOr(strings.TrimSpace(ans.ID), acc.ID, "<id>"), true); !fileOrLinkExists(generated) {
		options = append(options, huh.NewOption("Generate a separate signing key: "+a.env.Shorten(generated), keyGenerate))
	}
	for _, k := range discovered {
		if a.env.Shorten(keys.PublicPath(k)) != acc.SigningKey {
			options = append(options, huh.NewOption(keyLabel(a.env.Shorten(k)), k))
		}
	}
	return append(options,
		huh.NewOption("Another key file…", keyPathEtc),
		huh.NewOption("Don't sign", keyNone))
}

// keyPathValidator checks a typed key file the way --auth-key and
// --signing-key do.
func (a *app) keyPathValidator(signing bool) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("enter a key file")
		}
		_, err := a.keyPath(strings.TrimSpace(s), signing)
		return err
	}
}

// applyAnswers copies the saved answers into acc and works out the rest.
func (a *app) applyAnswers(ans *answers, acc *accounts.Account) wizardResult {
	acc.ID = cmpOr(strings.TrimSpace(ans.ID), acc.ID)
	acc.Name = cmpOr(strings.TrimSpace(ans.Name), acc.Name)
	acc.Email = cmpOr(strings.TrimSpace(ans.Email), acc.Email)
	acc.Hosts = splitList(ans.Hosts)
	if len(acc.Hosts) == 0 {
		acc.Hosts = []string{accounts.DefaultHost}
	}
	acc.GitHubUser = strings.TrimSpace(ans.GitHubUser)

	res := wizardResult{folders: splitList(ans.Folders), makeDefault: ans.Default}
	keyFile := func(choice, typed string) string {
		if choice == keyPathEtc {
			return typed
		}
		return a.env.Shorten(choice)
	}
	switch ans.Auth {
	case keyKeep:
	case keyGenerate:
		res.keys.generateAuth = true
	case keyNone:
		none := ""
		res.keys.auth = &none
	default:
		path := keyFile(ans.Auth, strings.TrimSpace(ans.AuthPath))
		res.keys.auth = &path
	}
	switch ans.Signing {
	case keyKeep:
	case keyNone:
		none := ""
		res.keys.signing = &none
		return res
	case keyWithAuth:
		res.keys.signWithAuth = true
	case keyGenerate:
		res.keys.generateSigning = true
	default:
		path := keyFile(ans.Signing, strings.TrimSpace(ans.SigningPath))
		res.keys.signing = &path
	}
	commits, tags := slices.Contains(ans.Sign, "commits"), slices.Contains(ans.Sign, "tags")
	res.keys.signCommits, res.keys.signTags = &commits, &tags
	return res
}

// review summarizes the answers before saving. It's redrawn as answers
// change, so it's always what Save would write.
func (a *app) review(ans *answers, acc *accounts.Account) string {
	sshDir := filepath.Join(a.env.Home, ".ssh")
	id := cmpOr(strings.TrimSpace(ans.ID), acc.ID)
	describe := func(choice, typed, current string, signing bool) string {
		switch choice {
		case keyKeep:
			return current
		case keyGenerate:
			return "a new key: " + a.env.Shorten(keys.DefaultPath(sshDir, id, signing))
		case keyPathEtc:
			return cmpOr(strings.TrimSpace(typed), "(file not chosen yet)")
		case keyNone:
			return ""
		}
		return a.env.Shorten(choice)
	}
	auth := cmpOr(describe(ans.Auth, ans.AuthPath, acc.AuthKey, false), "ssh's own keys")
	signing := describe(ans.Signing, ans.SigningPath, acc.SigningKey, true)
	switch {
	case ans.Signing == keyWithAuth:
		signing = "with the auth key"
	case signing == "":
		signing = "off"
	}
	if ans.Signing != keyNone {
		scope := strings.Join(ans.Sign, " and ")
		signing += " (" + cmpOr(scope, "nothing selected") + ")"
	}
	defaultNote := "no"
	if ans.Default {
		defaultNote = "yes"
	}
	rows := [][2]string{
		{"Account", id},
		{"Identity", cmpOr(strings.TrimSpace(ans.Name), acc.Name) + " <" + cmpOr(strings.TrimSpace(ans.Email), acc.Email) + ">"},
		{"Hosts", strings.Join(splitList(ans.Hosts), ", ")},
		{"Folders", cmpOr(strings.Join(splitList(ans.Folders), ", "), "none")},
		{"Default", defaultNote},
		{"Auth key", auth},
		{"Signing", signing},
	}
	if strings.TrimSpace(ans.GitHubUser) != "" {
		rows = append(rows[:3], append([][2]string{{"GitHub", strings.TrimSpace(ans.GitHubUser)}}, rows[3:]...)...)
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s %s\n", ui.Label.Width(10).Render(r[0]), r[1])
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.ColorPurple).
		Padding(0, 1).Render(strings.TrimRight(b.String(), "\n"))
}

func fileOrLinkExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// keepIfEmpty validates an answer, letting an empty one through when the
// field already has a value: that means "keep it". Huh's accessible prompts
// check the typed text before falling back to the current value.
func keepIfEmpty(current string, validate func(string) error) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" && current != "" {
			return nil
		}
		return validate(s)
	}
}

// cmpOr returns the first of its arguments that isn't empty.
func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// keyLabel names a discovered key in a list, noting agent-held ones.
func keyLabel(path string) string {
	if strings.HasSuffix(path, ".pub") {
		return path + " (private key in an agent)"
	}
	return path
}

// splitList splits a comma-separated answer.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// onlyWriteFlags reports whether no flags beyond --dry-run and --yes were
// given, so a command in a terminal should ask with the wizard instead.
func onlyWriteFlags(fs *flag.FlagSet) bool {
	only := true
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "dry-run" && f.Name != "yes" {
			only = false
		}
	})
	return only
}

// addWithWizard adds an account by asking for each setting. With no
// accounts yet, it offers to start from the identity in the global Git
// config.
func (a *app) addWithWizard(id string, w writeFlags) int {
	list, err := accounts.Load(a.env)
	if err != nil {
		return a.fail(err)
	}
	acc := &accounts.Account{ID: id, Hosts: []string{accounts.DefaultHost}}
	if len(list) == 0 {
		if found, source, ok := accounts.FromGlobal(a.env); ok {
			start := true
			question := fmt.Sprintf("Start from the identity in %s: %s <%s>?", a.env.Shorten(source), found.Name, found.Email)
			if err := a.runForm(huh.NewForm(huh.NewGroup(
				huh.NewConfirm().Title(question).
					Description("Its name, email and SSH keys become your first account, the default").
					Affirmative("Yes").Negative("No, start fresh").Value(&start),
			))); err != nil {
				return a.fail(err)
			}
			if start {
				found.ID = id
				acc = found
			}
		}
	}
	res, err := a.accountWizard(list, acc, true)
	if err != nil {
		return a.fail(err)
	}
	pending, err := a.applyKeyChanges(acc, res.keys)
	if err != nil {
		return a.fail(err)
	}
	if err := a.bindFolders(list, acc, res.folders, w); err != nil {
		return a.fail(err)
	}
	list = append(list, acc)
	if res.makeDefault || len(list) == 1 {
		accounts.SetDefault(list, acc)
	}
	a.warnSharedKeys(list)
	return a.save(list, w, fmt.Sprintf("Added account %s", acc.ID), pending...)
}

// editWithWizard changes an account by walking through its settings.
func (a *app) editWithWizard(list []*accounts.Account, acc *accounts.Account, w writeFlags) int {
	res, err := a.accountWizard(list, acc, false)
	if err != nil {
		return a.fail(err)
	}
	pending, err := a.applyKeyChanges(acc, res.keys)
	if err != nil {
		return a.fail(err)
	}
	acc.Folders = nil
	if err := a.bindFolders(list, acc, res.folders, w); err != nil {
		return a.fail(err)
	}
	if res.makeDefault {
		accounts.SetDefault(list, acc)
	} else {
		acc.Default = false
	}
	a.warnSharedKeys(list)
	return a.save(list, w, fmt.Sprintf("Updated account %s", acc.ID), pending...)
}
