package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/ops"
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

	goingBack bool // the last key pressed moves back a page (trackDirection)

	form        *huh.Form             // the full-screen wizard, for which field has focus
	completions map[string]completion // the inputs that suggest paths, by field key
}

// filter sees every message before the full-screen wizard does: Tab accepts
// a path suggestion, and the direction the user moves in is noted.
func (ans *answers) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	msg = ans.completeOnTab(msg)
	ans.trackDirection(msg)
	return msg
}

// trackDirection notes, for every message the form gets, whether the user
// is moving back.
func (ans *answers) trackDirection(msg tea.Msg) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		ans.goingBack = key.Matches(k, formKeyMap().Input.Prev)
	}
}

// forward makes a page's check apply only when the user moves on. Huh
// checks a field again as it loses focus, and won't leave a page holding an
// error in either direction, so a half-typed answer would otherwise trap the
// user on its page. Moving on still needs a valid answer, and the Save
// button checks every page once more (checkAnswers), in case a page left
// with a bad answer isn't passed again.
func forward[T any](ans *answers, check func(T) error) func(T) error {
	return func(v T) error {
		if ans.goingBack {
			return nil
		}
		return check(v)
	}
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
	// The wizard's filter turns Tab into Ctrl+E while a suggestion shows
	// (completeOnTab), so that's the key to show.
	km.Input.AcceptSuggestion.SetHelp("tab", "complete")
	km.Select.Filter.SetEnabled(false)
	km.MultiSelect.Filter.SetEnabled(false)
	return km
}

// runSteps runs the wizard's pages as one form, so Shift+Tab goes back to
// any earlier page and Esc cancels from any of them. Accessible prompts
// can't go back, and Huh's accessible mode ignores hidden pages, so there
// each visible page is asked in turn instead.
func (a *app) runSteps(ans *answers, steps []step) error {
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
	return a.runForm(wizardForm(ans, steps))
}

// wizardForm puts the wizard's pages into one form, which tells the pages'
// checks which way the user is moving.
func wizardForm(ans *answers, steps []step) *huh.Form {
	groups := make([]*huh.Group, len(steps))
	for i, st := range steps {
		groups[i] = st.group()
		if st.hide != nil {
			groups[i] = groups[i].WithHideFunc(st.hide)
		}
	}
	ans.form = huh.NewForm(groups...).WithKeyMap(formKeyMap()).WithProgramOptions(tea.WithFilter(ans.filter))
	return ans.form
}

// accountWizard walks through an account's settings, starting from acc: an
// account being edited, or a new one (maybe prefilled from the global Git
// config). It returns the answers once the user saves; addRequest and
// editRequest turn them into the change to make. acc isn't changed.
func (a *app) accountWizard(list []*accounts.Account, acc *accounts.Account, isNew bool) (*answers, error) {
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
	if err := a.runSteps(ans, steps); err != nil {
		return nil, err
	}
	if !ans.Save {
		return nil, errCancelled
	}
	return ans, nil
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
	anyGitHub := func() bool { return a.github.Any(splitList(ans.Hosts)) }
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
		a.completePaths(ans, huh.NewInput().Key("folders").Title("Folders").
			Description("Repos inside these folders use this account. Separate them with commas; leave empty for none").
			Placeholder("~/projects/work").
			Value(&ans.Folders).Validate(forward(ans, a.checkFolders)),
			"folders", &ans.Folders, a.folderSuggestions),
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
					Value(&ans.Hosts).Validate(forward(ans, checkHosts)),
			).Title("Hosts")
		}},
		{group: func() *huh.Group {
			return huh.NewGroup(
				huh.NewInput().Key("github-user").Title("GitHub username").
					Description("Optional. doppel checks the keys log in as this user, and uploads keys to it").
					Value(&ans.GitHubUser).Validate(forward(ans, accounts.ValidateGitHubUser)),
			).Title("Hosts")
		}, hide: func() bool { return !anyGitHub() }},
		{group: func() *huh.Group { return huh.NewGroup(where...).Title("Where it applies") }},
		{group: func() *huh.Group {
			return huh.NewGroup(
				a.liveDescription(huh.NewSelect[string]().Key("auth").Title("Auth key").
					Options(a.authOptions(acc, discovered)...).
					Value(&ans.Auth).Validate(forward(ans, func(string) error { return a.checkAuth(ans, acc) })),
					func() string {
						return "The SSH key this account fetches and pushes with. A key of its own keeps hosts from mixing up your accounts. " +
							"Generate creates " + a.env.Shorten(keys.DefaultPath(sshDir, ans.accountID(acc), false))
					}, &struct{ ID *string }{&ans.ID}),
			).Title("Keys")
		}},
		{group: func() *huh.Group {
			return huh.NewGroup(
				a.completePaths(ans, huh.NewInput().Key("auth-path").Title("Auth key file").
					Description("A private key, or a .pub whose private key lives in an agent").
					Placeholder("~/.ssh/id_ed25519").Value(&ans.AuthPath).
					Validate(forward(ans, a.keyPathValidator(false))),
					"auth-path", &ans.AuthPath, a.keySuggestions),
			).Title("Keys")
		}, hide: func() bool { return ans.Auth != keyPathEtc }},
		{group: func() *huh.Group {
			return huh.NewGroup(
				a.liveDescription(huh.NewSelect[string]().Key("signing").Title("Signing").
					Options(a.signingOptions(acc, discovered)...).
					Value(&ans.Signing).Validate(forward(ans, func(string) error { return a.checkSigning(ans, acc) })),
					func() string {
						return "Signed commits show as Verified on GitHub and GitLab. " +
							"Generate creates " + a.env.Shorten(keys.DefaultPath(sshDir, ans.accountID(acc), true))
					}, &struct{ ID *string }{&ans.ID}),
			).Title("Keys")
		}},
		{group: func() *huh.Group {
			return huh.NewGroup(
				a.completePaths(ans, huh.NewInput().Key("signing-path").Title("Signing key file").
					Description("The key's .pub has to be next to it").
					Placeholder("~/.ssh/id_ed25519").Value(&ans.SigningPath).
					Validate(forward(ans, a.keyPathValidator(true))),
					"signing-path", &ans.SigningPath, a.keySuggestions),
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
				huh.NewConfirm().Key("save").Title("Save this account?").Affirmative("Save").Negative("Cancel").Value(&ans.Save).
					Validate(forward(ans, func(save bool) error {
						if !save {
							return nil
						}
						return a.checkAnswers(ans, acc, anyGitHub)
					})),
			)
		}},
	}
	return ans, steps
}

// liveDescription gives a select a description that follows earlier
// answers. Huh's accessible mode asks one page at a time, so there the
// description at that moment is enough.
func (a *app) liveDescription(sel *huh.Select[string], text func() string, binding any) *huh.Select[string] {
	if a.accessible {
		return sel.Description(text())
	}
	return sel.DescriptionFunc(text, binding)
}

// liveNote gives a note text that follows earlier answers. Huh formats a
// note's text in the full-screen form (_ for italics, * for bold), so it is
// escaped there to show exactly as written; accessible prompts print it as
// it is.
func (a *app) liveNote(note *huh.Note, text func() string, binding any) *huh.Note {
	if a.accessible {
		return note.Description(text())
	}
	return note.DescriptionFunc(func() string { return escapeNote(text()) }, binding)
}

// escapeNote protects text from Huh's note formatting.
func escapeNote(text string) string {
	return strings.NewReplacer(`\`, `\\`, "_", `\_`, "*", `\*`, "`", "\\`").Replace(text)
}

// accountID is the account's ID as answered so far, for naming its keys.
func (ans *answers) accountID(acc *accounts.Account) string {
	return cmpOr(strings.TrimSpace(ans.ID), acc.ID, "<id>")
}

// The key choices are the same whatever the earlier answers are, and a
// choice that doesn't fit them is refused when the user moves on. When a
// select's options change, Huh keeps the cursor where it was, so hiding the
// chosen option would quietly answer with whichever one took its place.

// authOptions lists the auth key choices: keep the current key, generate
// one, a key found in ~/.ssh, another file, or none.
func (a *app) authOptions(acc *accounts.Account, discovered []keys.Ref) []huh.Option[string] {
	var options []huh.Option[string]
	if acc.AuthKey != "" {
		options = append(options, huh.NewOption("Keep "+acc.AuthKey.Display(), keyKeep))
	}
	options = append(options, huh.NewOption("Generate a new key", keyGenerate))
	for _, k := range discovered {
		if short := k.Map(a.env.Shorten); short != acc.AuthKey {
			options = append(options, huh.NewOption(keyLabel(short), string(k)))
		}
	}
	return append(options,
		huh.NewOption("Another key file…", keyPathEtc),
		huh.NewOption("None: use ssh's own keys", keyNone))
}

// signingOptions lists the signing choices: keep the current key, sign with
// the auth key, generate a separate key, a key found in ~/.ssh, another
// file, or don't sign.
func (a *app) signingOptions(acc *accounts.Account, discovered []keys.Ref) []huh.Option[string] {
	var options []huh.Option[string]
	if acc.SigningKey != "" {
		options = append(options, huh.NewOption("Keep "+acc.SigningKey.Display(), keyKeep))
	}
	options = append(options,
		huh.NewOption("Sign with the auth key", keyWithAuth),
		huh.NewOption("Generate a separate signing key", keyGenerate))
	for _, k := range discovered {
		if short := k.Map(a.env.Shorten); short.Public() != acc.SigningKey {
			options = append(options, huh.NewOption(keyLabel(short), string(k)))
		}
	}
	return append(options,
		huh.NewOption("Another key file…", keyPathEtc),
		huh.NewOption("Don't sign", keyNone))
}

// checkAuth refuses an auth choice that doesn't fit the other answers.
func (a *app) checkAuth(ans *answers, acc *accounts.Account) error {
	if ans.Auth == keyGenerate {
		return a.checkGenerate(keys.DefaultPath(filepath.Join(a.env.Home, ".ssh"), ans.accountID(acc), false))
	}
	return nil
}

// checkSigning refuses a signing choice that doesn't fit the other answers.
func (a *app) checkSigning(ans *answers, acc *accounts.Account) error {
	switch ans.Signing {
	case keyWithAuth:
		if ans.Auth == keyNone {
			return errors.New("there's no auth key to sign with: pick another key, or go back and choose one")
		}
	case keyGenerate:
		return a.checkGenerate(keys.DefaultPath(filepath.Join(a.env.Home, ".ssh"), ans.accountID(acc), true))
	}
	return nil
}

// checkGenerate refuses to generate a key where one already exists.
func (a *app) checkGenerate(path string) error {
	if fileOrLinkExists(path) {
		return fmt.Errorf("%s already exists, and doppel never overwrites keys: pick it from the list instead", a.env.Shorten(path))
	}
	return nil
}

func checkHosts(s string) error {
	for _, h := range splitList(s) {
		if err := accounts.ValidateHost(h); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) checkFolders(s string) error {
	for _, f := range splitList(s) {
		if _, _, err := a.env.NormalizeFolder(f, a.cwd); err != nil {
			return err
		}
	}
	return nil
}

// checkAnswers checks every page that's showing once more before saving.
// The pages check their answers only when the user moves on, so one left by
// going back could still hold a bad answer.
func (a *app) checkAnswers(ans *answers, acc *accounts.Account, anyGitHub func() bool) error {
	checks := []struct {
		page string
		show bool
		err  func() error
	}{
		{"Git hosts", true, func() error { return checkHosts(ans.Hosts) }},
		{"GitHub username", anyGitHub(), func() error { return accounts.ValidateGitHubUser(ans.GitHubUser) }},
		{"Folders", true, func() error { return a.checkFolders(ans.Folders) }},
		{"Auth key", true, func() error { return a.checkAuth(ans, acc) }},
		{"Auth key file", ans.Auth == keyPathEtc, func() error { return a.keyPathValidator(false)(ans.AuthPath) }},
		{"Signing", true, func() error { return a.checkSigning(ans, acc) }},
		{"Signing key file", ans.Signing == keyPathEtc, func() error { return a.keyPathValidator(true)(ans.SigningPath) }},
	}
	for _, c := range checks {
		if !c.show {
			continue
		}
		if err := c.err(); err != nil {
			return fmt.Errorf("%s: %w. Go back with Shift+Tab to fix it", c.page, err)
		}
	}
	return nil
}

// keyPathValidator checks a typed key file the way --auth-key and
// --signing-key do.
func (a *app) keyPathValidator(signing bool) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("enter a key file")
		}
		_, err := ops.KeyPath(a.opsContext(writeFlags{}), strings.TrimSpace(s), signing)
		return err
	}
}

// addRequest turns the saved answers into the request that adds the
// account, starting from start: a blank account, or one read from the
// global Git config, whose keys the "keep" choices keep.
func (a *app) addRequest(ans *answers, start *accounts.Account) ops.AddRequest {
	acc := *start
	acc.ID, acc.Name, acc.Email = a.answeredIdentity(ans, start)
	acc.Hosts = answeredHosts(ans)
	if user, ok := a.answeredGitHubUser(ans); ok {
		acc.GitHubUser = user
	}
	return ops.AddRequest{Account: &acc, Folders: splitList(ans.Folders), Default: ans.Default, Keys: a.answeredKeys(ans)}
}

// editRequest turns the saved answers into the request that changes acc.
// The wizard asks for every setting, so the request sets them all.
func (a *app) editRequest(ans *answers, acc *accounts.Account) ops.EditRequest {
	_, name, email := a.answeredIdentity(ans, acc)
	folders, makeDefault := splitList(ans.Folders), ans.Default
	req := ops.EditRequest{
		ID: acc.ID, Name: &name, Email: &email, Hosts: answeredHosts(ans),
		Folders: &folders, Default: &makeDefault, Keys: a.answeredKeys(ans),
	}
	if user, ok := a.answeredGitHubUser(ans); ok {
		req.GitHubUser = &user
	}
	return req
}

// answeredIdentity is the ID, name and email answered, or acc's for an
// answer left empty (accessible prompts keep a value that way).
func (a *app) answeredIdentity(ans *answers, acc *accounts.Account) (id, name, email string) {
	return cmpOr(strings.TrimSpace(ans.ID), acc.ID), cmpOr(strings.TrimSpace(ans.Name), acc.Name),
		cmpOr(strings.TrimSpace(ans.Email), acc.Email)
}

func answeredHosts(ans *answers) []string {
	if hosts := splitList(ans.Hosts); len(hosts) > 0 {
		return hosts
	}
	return []string{accounts.DefaultHost}
}

// answeredGitHubUser is the GitHub username answered. The page only shows
// for a GitHub host; when it's hidden, its answer (perhaps left half-typed
// by going back) doesn't count, and ok is false.
func (a *app) answeredGitHubUser(ans *answers) (user string, ok bool) {
	if !a.github.Any(answeredHosts(ans)) {
		return "", false
	}
	return strings.TrimSpace(ans.GitHubUser), true
}

// answeredKeys turns the key pages' answers into key changes.
func (a *app) answeredKeys(ans *answers) ops.KeyChanges {
	var ch ops.KeyChanges
	keyFile := func(choice, typed string) string {
		if choice == keyPathEtc {
			return strings.TrimSpace(typed)
		}
		return a.env.Shorten(choice)
	}
	switch ans.Auth {
	case keyKeep:
	case keyGenerate:
		ch.GenerateAuth = true
	case keyNone:
		none := ""
		ch.Auth = &none
	default:
		path := keyFile(ans.Auth, ans.AuthPath)
		ch.Auth = &path
	}
	switch ans.Signing {
	case keyKeep:
	case keyNone:
		none := ""
		ch.Signing = &none
		return ch
	case keyWithAuth:
		ch.SignWithAuth = true
	case keyGenerate:
		ch.GenerateSigning = true
	default:
		path := keyFile(ans.Signing, ans.SigningPath)
		ch.Signing = &path
	}
	commits, tags := slices.Contains(ans.Sign, "commits"), slices.Contains(ans.Sign, "tags")
	ch.SignCommits, ch.SignTags = &commits, &tags
	return ch
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
	auth := cmpOr(describe(ans.Auth, ans.AuthPath, acc.AuthKey.Display(), false), "ssh's own keys")
	signing := describe(ans.Signing, ans.SigningPath, acc.SigningKey.Display(), true)
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
func keyLabel(key keys.Ref) string {
	if key.IsPublic() {
		return key.Display() + " (private key in an agent)"
	}
	return key.Display()
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

// renderDiscovered renders a summary card of discovered accounts.
func renderDiscovered(disc *accounts.Discovered) string {
	var b strings.Builder
	for i, da := range disc.Accounts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		idLine := ui.Title.Render(da.Account.ID)
		if da.Account.Default {
			idLine += " " + ui.Star.Render("★") + " " + ui.Dim.Render("(default)")
		}
		fmt.Fprintf(&b, "%s\n", idLine)
		fmt.Fprintf(&b, "%s <%s>\n", da.Account.Name, da.Account.Email)
		if len(da.Account.Folders) > 0 {
			fmt.Fprintf(&b, "%s %s", ui.Label.Render("Repos:"), strings.Join(da.Account.Folders, ", "))
		} else {
			fmt.Fprintf(&b, "%s %s", ui.Label.Render("Repos:"), "outside every folder")
		}
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.ColorPurple).
		Padding(0, 1).Render(b.String())
}

// promptImport asks the user whether to import discovered accounts.
func (a *app) promptImport(disc *accounts.Discovered) (bool, error) {
	a.printf("\n%s %s\n%s\n\n",
		ui.BadgeAccent.Render(" DOPPEL "),
		ui.Title.Render("Import Git accounts"),
		ui.Dim.Render("Existing multi-account setup detected in your Git configuration:"),
	)
	a.printf("%s\n\n", renderDiscovered(disc))
	use := true
	question := "Import these Git accounts into doppel?"
	err := a.runForm(huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(question).
			Description("Account files will be created and imported includeIf directives backed up").
			Affirmative("Yes, import accounts").
			Negative("No, start fresh").
			Value(&use),
	)))
	if err != nil {
		return false, err
	}
	return use, nil
}

// addWithWizard adds an account by asking for each setting. With no
// accounts yet, it offers to start from the identity in the global Git
// config.
func (a *app) addWithWizard(id string, w writeFlags) (*ops.Result, error) {
	return a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		start := &accounts.Account{ID: id, Hosts: []string{accounts.DefaultHost}}
		if len(list) == 0 {
			if !a.importDeclined {
				disc, err := accounts.Discover(a.env)
				if err == nil && len(disc.Accounts) >= 2 {
					use, err := a.promptImport(disc)
					if err != nil {
						return nil, err
					}
					if use {
						return ops.Import(ctx, ops.ImportRequest{Discovered: disc})
					}
					a.importDeclined = true
				}
			}
			if !a.importDeclined {
				if found, source, ok := accounts.FromGlobal(a.env); ok {
					use := true
					question := fmt.Sprintf("Start from the identity in %s: %s <%s>?", a.env.Shorten(source), found.Name, found.Email)
					if err := a.runForm(huh.NewForm(huh.NewGroup(
						huh.NewConfirm().Title(question).
							Description("Its name, email and SSH keys become your first account, the default").
							Affirmative("Yes").Negative("No, start fresh").Value(&use),
					))); err != nil {
						return nil, err
					}
					if use {
						found.ID = id
						start = found
					}
				}
			}
		}
		ans, err := a.accountWizard(list, start, true)
		if err != nil {
			return nil, err
		}
		return ops.Add(ctx, list, a.addRequest(ans, start))
	})
}

// editWithWizard changes an account by walking through its settings.
func (a *app) editWithWizard(id string, w writeFlags) (*ops.Result, error) {
	return a.change(w, func(ctx ops.Context, list []*accounts.Account) (*ops.Change, error) {
		acc, err := ops.Find(list, id)
		if err != nil {
			return nil, err
		}
		ans, err := a.accountWizard(list, acc, false)
		if err != nil {
			return nil, err
		}
		return ops.Edit(ctx, list, a.editRequest(ans, acc))
	})
}
