package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

// accountWizard walks through an account's settings, starting from acc: an
// account being edited, or a new one (maybe prefilled from the global Git
// config). It edits acc's fields in place and returns the changes that need
// more than a field: keys, folders and the default.
func (a *app) accountWizard(list []*accounts.Account, acc *accounts.Account, isNew bool) (wizardResult, error) {
	var res wizardResult
	title := "Edit account " + acc.ID
	if isNew {
		title = "New account"
	}
	a.printf("\n%s %s\n%s\n\n", ui.BadgeAccent.Render(" DOPPEL "), ui.Title.Render(title),
		ui.Dim.Render("A Git identity, its SSH keys, and the folders where it applies."))

	// Identity
	prevID, prevName, prevEmail := acc.ID, acc.Name, acc.Email
	var fields []huh.Field
	if isNew {
		fields = append(fields, huh.NewInput().Title("Account ID").
			Description("A short name, like work or personal").
			Value(&acc.ID).
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
	fields = append(fields,
		huh.NewInput().Title("Name").Description("The author name on your commits").Value(&acc.Name).
			Validate(keepIfEmpty(prevName, func(s string) error {
				if s == "" {
					return errors.New("a name is required")
				}
				return nil
			})),
		huh.NewInput().Title("Email").Description("The author email on your commits").Value(&acc.Email).
			Validate(keepIfEmpty(prevEmail, accounts.ValidateEmail)),
	)
	if err := a.runForm(huh.NewForm(huh.NewGroup(fields...).Title("Identity"))); err != nil {
		return res, err
	}
	acc.ID = cmpOr(strings.TrimSpace(acc.ID), prevID)
	acc.Name = cmpOr(strings.TrimSpace(acc.Name), prevName)
	acc.Email = cmpOr(strings.TrimSpace(acc.Email), prevEmail)

	// Hosts
	hosts := strings.Join(acc.Hosts, ", ")
	if hosts == "" {
		hosts = accounts.DefaultHost
	}
	if err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Git hosts").Description("Where this account pushes, separated by commas").
			Value(&hosts).Validate(func(s string) error {
			for _, h := range splitList(s) {
				if err := accounts.ValidateHost(h); err != nil {
					return err
				}
			}
			return nil
		}),
	).Title("Hosts"))); err != nil {
		return res, err
	}
	acc.Hosts = splitList(hosts)
	if len(acc.Hosts) == 0 {
		acc.Hosts = []string{accounts.DefaultHost}
	}
	if slices.ContainsFunc(acc.Hosts, func(h string) bool { return strings.EqualFold(h, "github.com") }) {
		if err := a.runForm(huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("GitHub username").
				Description("Optional. doppel checks the keys log in as this user").
				Value(&acc.GitHubUser).Validate(accounts.ValidateGitHubUser),
		))); err != nil {
			return res, err
		}
	}

	// Folders and default
	folders := strings.Join(acc.Folders, ", ")
	others := len(list) - 1
	if isNew {
		others = len(list)
	}
	res.makeDefault = acc.Default || others == 0
	group := []huh.Field{
		huh.NewInput().Title("Folders").
			Description("Repos inside these folders use this account. Separate them with commas; leave empty for none").
			Placeholder("~/projects/work").
			Value(&folders).Validate(func(s string) error {
			for _, f := range splitList(s) {
				if _, _, err := a.env.NormalizeFolder(f, a.cwd); err != nil {
					return err
				}
			}
			return nil
		}),
	}
	if others > 0 {
		group = append(group, huh.NewConfirm().Title("Make it the default account?").
			Description("The default account is used for repos outside every folder").
			Affirmative("Yes").Negative("No").Value(&res.makeDefault))
	}
	if err := a.runForm(huh.NewForm(huh.NewGroup(group...).Title("Where it applies"))); err != nil {
		return res, err
	}
	res.folders = splitList(folders)

	// Keys
	sshDir := filepath.Join(a.env.Home, ".ssh")
	discovered := keys.Discover(sshDir)
	authChoice, err := a.chooseAuthKey(acc, isNew, discovered, sshDir)
	if err != nil {
		return res, err
	}
	res.keys = a.authChanges(authChoice)
	if err := a.chooseSigning(acc, isNew, authChoice, discovered, sshDir, &res.keys); err != nil {
		return res, err
	}

	// Review
	a.printf("\n%s\n", a.reviewCard(acc, res))
	save := true
	if err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title("Save this account?").Affirmative("Save").Negative("Cancel").Value(&save),
	))); err != nil {
		return res, err
	}
	if !save {
		return res, errCancelled
	}
	return res, nil
}

// chooseAuthKey asks which key the account logs in with.
func (a *app) chooseAuthKey(acc *accounts.Account, isNew bool, discovered []string, sshDir string) (string, error) {
	// Keep the current key; a new account gets a key of its own.
	var options []huh.Option[string]
	choice := keyNone
	if acc.AuthKey != "" {
		options = append(options, huh.NewOption("Keep "+acc.AuthKey, keyKeep))
		choice = keyKeep
	}
	generated := keys.DefaultPath(sshDir, acc.ID, false)
	if _, err := os.Lstat(generated); err != nil {
		options = append(options, huh.NewOption("Generate a new key: "+a.env.Shorten(generated), keyGenerate))
		if isNew && choice == keyNone {
			choice = keyGenerate
		}
	}
	for _, k := range discovered {
		if a.env.Shorten(k) == acc.AuthKey {
			continue
		}
		options = append(options, huh.NewOption(keyLabel(a.env.Shorten(k)), k))
	}
	options = append(options,
		huh.NewOption("Another key file…", keyPathEtc),
		huh.NewOption("None: use ssh's own keys", keyNone))

	if err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Auth key").
			Description("The SSH key this account fetches and pushes with. A key of its own keeps hosts from mixing up your accounts").
			Options(options...).Value(&choice),
	).Title("Keys"))); err != nil {
		return "", err
	}
	if choice != keyPathEtc {
		return choice, nil
	}
	return a.askKeyPath("Auth key file", "A private key, or a .pub whose private key lives in an agent", false)
}

// authChanges turns the auth key choice into key changes.
func (a *app) authChanges(choice string) keyChanges {
	var ch keyChanges
	switch choice {
	case keyKeep:
	case keyGenerate:
		ch.generateAuth = true
	case keyNone:
		none := ""
		ch.auth = &none
	default:
		path := a.env.Shorten(choice)
		ch.auth = &path
	}
	return ch
}

// chooseSigning asks whether and how the account signs commits and tags.
func (a *app) chooseSigning(acc *accounts.Account, isNew bool, authChoice string, discovered []string, sshDir string, ch *keyChanges) error {
	// Keep the current setup; a new account with an auth key signs with it.
	var options []huh.Option[string]
	choice := keyNone
	if acc.SigningKey != "" {
		options = append(options, huh.NewOption("Keep "+acc.SigningKey, keyKeep))
		choice = keyKeep
	}
	if authChoice != keyNone {
		options = append(options, huh.NewOption("Sign with the auth key", keyWithAuth))
		if isNew && choice == keyNone {
			choice = keyWithAuth
		}
	}
	generated := keys.DefaultPath(sshDir, acc.ID, true)
	if _, err := os.Lstat(generated); err != nil {
		options = append(options, huh.NewOption("Generate a separate signing key: "+a.env.Shorten(generated), keyGenerate))
	}
	for _, k := range discovered {
		pub := a.env.Shorten(keys.PublicPath(k))
		if pub == acc.SigningKey {
			continue
		}
		options = append(options, huh.NewOption(keyLabel(a.env.Shorten(k)), k))
	}
	options = append(options,
		huh.NewOption("Another key file…", keyPathEtc),
		huh.NewOption("Don't sign", keyNone))

	if err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Signing").
			Description("Signed commits show as Verified on GitHub and GitLab").
			Options(options...).Value(&choice),
	))); err != nil {
		return err
	}
	if choice == keyPathEtc {
		path, err := a.askKeyPath("Signing key file", "The key's .pub has to be next to it", true)
		if err != nil {
			return err
		}
		choice = path
	}
	switch choice {
	case keyKeep:
	case keyNone:
		none := ""
		ch.signing = &none
		return nil
	case keyWithAuth:
		ch.signWithAuth = true
	case keyGenerate:
		ch.generateSigning = true
	default:
		path := a.env.Shorten(choice)
		ch.signing = &path
	}

	what := []string{}
	if acc.SignCommits || acc.SigningKey == "" {
		what = append(what, "commits")
	}
	if acc.SignTags || acc.SigningKey == "" {
		what = append(what, "tags")
	}
	if err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().Title("Sign").
			Options(huh.NewOption("Commits", "commits"), huh.NewOption("Tags", "tags")).
			Value(&what),
	))); err != nil {
		return err
	}
	commits, tags := slices.Contains(what, "commits"), slices.Contains(what, "tags")
	ch.signCommits, ch.signTags = &commits, &tags
	return nil
}

// askKeyPath asks for a key file, checking it the way --auth-key and
// --signing-key do.
func (a *app) askKeyPath(title, description string, signing bool) (string, error) {
	var path string
	err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(title).Description(description).Placeholder("~/.ssh/id_ed25519").
			Value(&path).Validate(func(s string) error {
			_, err := a.keyPath(strings.TrimSpace(s), signing)
			if strings.TrimSpace(s) == "" {
				return errors.New("enter a key file")
			}
			return err
		}),
	)))
	return strings.TrimSpace(path), err
}

// reviewCard summarizes an account before it's saved.
func (a *app) reviewCard(acc *accounts.Account, res wizardResult) string {
	describe := func(choice string, generate bool, keep string) string {
		switch {
		case generate:
			return "a new key"
		case choice != "":
			return choice
		case keep != "":
			return keep
		}
		return "none"
	}
	auth := describe(deref(res.keys.auth), res.keys.generateAuth, acc.AuthKey)
	if res.keys.auth != nil && *res.keys.auth == "" {
		auth = "ssh's own keys"
	}
	signing := describe(deref(res.keys.signing), res.keys.generateSigning, acc.SigningKey)
	switch {
	case res.keys.signWithAuth:
		signing = "the auth key"
	case res.keys.signing != nil && *res.keys.signing == "":
		signing = "off"
	}
	folders := "none"
	if len(res.folders) > 0 {
		folders = strings.Join(res.folders, ", ")
	}
	defaultNote := "no"
	if res.makeDefault {
		defaultNote = "yes"
	}
	rows := [][2]string{
		{"Account", acc.ID},
		{"Identity", acc.Name + " <" + acc.Email + ">"},
		{"Hosts", strings.Join(acc.Hosts, ", ")},
		{"Folders", folders},
		{"Default", defaultNote},
		{"Auth key", auth},
		{"Signing", signing},
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s %s\n", ui.Label.Width(10).Render(r[0]), r[1])
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.ColorPurple).
		Padding(0, 1).Render(strings.TrimRight(b.String(), "\n"))
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
