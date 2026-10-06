package cli

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vehkiya/doppel/internal/accounts"
)

// formDriver feeds keys to a Huh form the way a terminal would, running the
// commands each key returns so focus moves as it does for real.
type formDriver struct {
	t    *testing.T
	form *huh.Form
	// filter sees every message before the form, as the wizard's
	// tea.WithFilter does when it runs in a terminal.
	filter func(tea.Model, tea.Msg) tea.Msg
}

func newFormDriver(t *testing.T, form *huh.Form, filter func(tea.Model, tea.Msg) tea.Msg) *formDriver {
	t.Helper()
	d := &formDriver{t: t, form: form, filter: filter}
	d.feed(form.Init())
	return d
}

func (d *formDriver) feed(cmd tea.Cmd) {
	queue := runCmd(cmd)
	for i := 0; len(queue) > 0 && i < 200; i++ {
		msg := queue[0]
		queue = queue[1:]
		if d.filter != nil {
			msg = d.filter(nil, msg) // the wizard's filter doesn't look at the model
		}
		model, next := d.form.Update(msg)
		d.form = model.(*huh.Form)
		queue = append(queue, runCmd(next)...)
	}
}

// runCmd runs a command and returns the messages it produces, unpacking
// batches and sequences. Timers, like the cursor blink, are skipped: they
// don't move focus and would only slow the test down.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return nil
	}
	if msg == nil {
		return nil
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		var out []tea.Msg
		for i := 0; i < v.Len(); i++ {
			out = append(out, runCmd(v.Index(i).Interface().(tea.Cmd))...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// typeText types text one key at a time.
func (d *formDriver) typeText(text string) {
	for _, r := range text {
		d.feed(func() tea.Msg { return tea.KeyPressMsg{Code: r, Text: string(r)} })
	}
}

// press presses a key, such as tea.KeyEnter, or 'u' with tea.ModCtrl.
func (d *formDriver) press(code rune, mod ...tea.KeyMod) {
	k := tea.KeyPressMsg{Code: code}
	for _, m := range mod {
		k.Mod |= m
	}
	d.feed(func() tea.Msg { return k })
}

func (d *formDriver) focused() string {
	d.t.Helper()
	if f := d.form.GetFocusedField(); f != nil {
		return f.GetKey()
	}
	return ""
}

// newWizard builds the add wizard's form for a sandbox without accounts.
func newWizard(t *testing.T) (*sandbox, *answers, *formDriver) {
	t.Helper()
	return newWizardAfter(t, nil)
}

// newWizardAfter is newWizard, with setup run on the sandbox first, for
// example to create keys the wizard should find.
func newWizardAfter(t *testing.T, setup func(s *sandbox)) (*sandbox, *answers, *formDriver) {
	t.Helper()
	s := newSandbox(t)
	if setup != nil {
		setup(s)
	}
	a := s.newApp(s.Home)
	a.accessible = false
	ans, steps := a.wizardSteps(nil, &accounts.Account{Hosts: []string{accounts.DefaultHost}}, true)
	return s, ans, newFormDriver(t, wizardForm(ans, steps), ans.filter)
}

func TestWizardGoesBack(t *testing.T) {
	_, ans, d := newWizard(t)
	for _, step := range []struct{ text, next string }{
		{"work", "name"}, {"Jane Doe", "email"}, {"jane@acme.com", "hosts"},
	} {
		d.typeText(step.text)
		d.press(tea.KeyEnter)
		if got := d.focused(); got != step.next {
			t.Fatalf("after %q, focus is on %q, want %q", step.text, got, step.next)
		}
	}
	// Shift+Tab goes back across pages, keeping what was typed.
	for _, want := range []string{"email", "name", "id"} {
		d.press(tea.KeyTab, tea.ModShift)
		if got := d.focused(); got != want {
			t.Fatalf("back: focus is on %q, want %q", got, want)
		}
	}
	if ans.ID != "work" || ans.Name != "Jane Doe" || ans.Email != "jane@acme.com" {
		t.Errorf("answers after going back = %+v", ans)
	}
}

func TestWizardCancelsFromAnyPage(t *testing.T) {
	for _, pages := range []int{0, 1, 4} {
		_, _, d := newWizard(t)
		for _, text := range []string{"work", "Jane Doe", "jane@acme.com", "", ""}[:pages] {
			d.typeText(text)
			d.press(tea.KeyEnter)
		}
		d.press(tea.KeyEscape)
		if d.form.State != huh.StateAborted {
			t.Errorf("Esc after %d answers: form state %v, want aborted", pages, d.form.State)
		}
	}
}

func TestWizardSkipsTheGitHubPageForOtherHosts(t *testing.T) {
	for host, next := range map[string]string{"gitlab.com": "folders", "github.com": "github-user"} {
		_, _, d := newWizard(t)
		for _, text := range []string{"work", "Jane Doe", "jane@acme.com"} {
			d.typeText(text)
			d.press(tea.KeyEnter)
		}
		d.press('u', tea.ModCtrl) // clear the prefilled github.com
		d.typeText(host)
		d.press(tea.KeyEnter)
		if got := d.focused(); got != next {
			t.Errorf("hosts %s: next page is %q, want %q", host, got, next)
		}
	}
}

// fillIdentity answers the first three pages of a new account's wizard.
func fillIdentity(d *formDriver) {
	for _, text := range []string{"work", "Jane Doe", "jane@acme.com"} {
		d.typeText(text)
		d.press(tea.KeyEnter)
	}
}

// advanceTo presses Enter until the form is on the field with the given key.
func (d *formDriver) advanceTo(key string) {
	d.t.Helper()
	for i := 0; i < 20 && d.focused() != key; i++ {
		d.press(tea.KeyEnter)
	}
	if got := d.focused(); got != key {
		d.t.Fatalf("pressing Enter never reached %q; focus is on %q", key, got)
	}
}

// backTo presses Shift+Tab until the form is on the field with the given key.
func (d *formDriver) backTo(key string) {
	d.t.Helper()
	for i := 0; i < 20 && d.focused() != key; i++ {
		d.press(tea.KeyTab, tea.ModShift)
	}
	if got := d.focused(); got != key {
		d.t.Fatalf("pressing Shift+Tab never reached %q; focus is on %q", key, got)
	}
}

// view is what the form shows on a tall terminal, without styling.
func (d *formDriver) view() string {
	d.feed(func() tea.Msg { return tea.WindowSizeMsg{Width: 100, Height: 60} })
	return ansi.Strip(d.form.View())
}

// choose picks the option n places below the current one in a select.
func (d *formDriver) choose(n int) {
	for range n {
		d.press(tea.KeyDown)
	}
	d.press(tea.KeyEnter)
}

func TestWizardFormWalksEveryPage(t *testing.T) {
	_, ans, d := newWizard(t)
	focus := []string{d.focused()}
	next := func() {
		d.press(tea.KeyEnter)
		focus = append(focus, d.focused())
	}
	for _, text := range []string{"work", "Jane Doe", "jane@acme.com"} {
		d.typeText(text)
		next()
	}
	next() // hosts: github.com
	d.typeText("jane-acme")
	next()
	d.typeText("~/projects/work")
	next()
	next() // auth key: generate one (the first choice)
	next() // signing: with the auth key (the first choice)
	next() // sign: commits and tags, as preselected

	// No page for a key file, as neither choice asked for one.
	want := []string{"id", "name", "email", "hosts", "github-user", "folders", "auth", "signing", "sign", "save"}
	if !slices.Equal(focus, want) {
		t.Errorf("pages visited = %q, want %q", focus, want)
	}
	d.press(tea.KeyEnter) // save
	if d.form.State != huh.StateCompleted {
		t.Fatalf("form state = %v after saving, want completed", d.form.State)
	}
	if ans.ID != "work" || ans.GitHubUser != "jane-acme" || ans.Folders != "~/projects/work" || ans.Auth != keyGenerate ||
		ans.Signing != keyWithAuth || !slices.Equal(ans.Sign, []string{"commits", "tags"}) || !ans.Save {
		t.Errorf("answers = %+v", *ans)
	}
}

func TestWizardChoicesFollowEarlierAnswers(t *testing.T) {
	t.Run("the generated key is named after the account ID, even after going back", func(t *testing.T) {
		_, _, d := newWizard(t)
		fillIdentity(d)
		d.advanceTo("auth")
		if view := d.view(); !strings.Contains(view, "Generate creates ~/.ssh/id_ed25519_work") {
			t.Fatalf("the auth page doesn't name the key after the ID:\n%s", view)
		}
		d.backTo("id")
		d.press('u', tea.ModCtrl)
		d.typeText("client-project-alpha-2026") // the description grows by a line
		d.advanceTo("auth")
		if view := d.view(); !strings.Contains(view, "id_ed25519_client-project-alpha-2026") || strings.Contains(view, "id_ed25519_work") ||
			!strings.Contains(view, "Generate a new key") {
			t.Errorf("the auth page still shows the old ID:\n%s", view)
		}
	})

	t.Run("signing with the auth key needs an auth key", func(t *testing.T) {
		_, ans, d := newWizard(t)
		fillIdentity(d)
		d.advanceTo("auth")
		d.choose(2) // Generate, Another key file…, None
		if ans.Auth != keyNone || d.focused() != "signing" {
			t.Fatalf("auth = %q, focus on %q; want none, then the signing page", ans.Auth, d.focused())
		}
		d.press(tea.KeyEnter) // Sign with the auth key
		if view := d.view(); d.focused() != "signing" || !strings.Contains(view, "there's no auth key to sign with") {
			t.Errorf("signing with a missing auth key was accepted (focus %q):\n%s", d.focused(), view)
		}
		d.choose(3) // Sign with the auth key, Generate a separate key, Another key file…, Don't sign
		if d.focused() != "save" {
			t.Errorf("after choosing not to sign, focus is on %q; want the Sign page skipped", d.focused())
		}
	})

	t.Run("the review shows the latest answers", func(t *testing.T) {
		_, _, d := newWizard(t)
		fillIdentity(d)
		d.advanceTo("save")
		view := d.view()
		for _, want := range []string{"work", "jane@acme.com"} {
			if !strings.Contains(view, want) {
				t.Errorf("review is missing %q:\n%s", want, view)
			}
		}
		d.backTo("email")
		d.press('u', tea.ModCtrl)
		d.typeText("jane@acme.io")
		d.advanceTo("save")
		if view := d.view(); !strings.Contains(view, "jane@acme.io") || strings.Contains(view, "jane@acme.com") {
			t.Errorf("review shows the old email:\n%s", view)
		}
	})
}

func TestWizardShowsAKeyFilePageOnlyWhenAsked(t *testing.T) {
	s, ans, d := newWizard(t)
	s.Key("id_other", "jane@acme.com", "")
	fillIdentity(d)
	d.advanceTo("auth")
	d.choose(1) // Another key file…
	if d.focused() != "auth-path" {
		t.Fatalf("choosing another key file leads to %q, want its page", d.focused())
	}
	// Huh won't leave a page that holds an error, so give it a valid file before going back.
	d.typeText("~/.ssh/id_other")
	d.backTo("auth")
	d.press(tea.KeyUp) // back to Generate: the key file page disappears
	d.press(tea.KeyEnter)
	if ans.Auth != keyGenerate || d.focused() != "signing" {
		t.Errorf("after choosing Generate: auth = %q, focus on %q; want the key file page skipped", ans.Auth, d.focused())
	}
}

// DOP-18: going back must always work, whatever the key file page holds.
func TestWizardGoesBackFromAnUnfinishedKeyFilePage(t *testing.T) {
	for name, typed := range map[string]string{"empty": "", "not a key": "~/no-such-key"} {
		t.Run(name, func(t *testing.T) {
			_, _, d := newWizard(t)
			fillIdentity(d)
			d.advanceTo("auth")
			d.choose(1) // Another key file…
			if d.focused() != "auth-path" {
				t.Fatalf("focus is on %q, want the key file page", d.focused())
			}
			d.typeText(typed)
			d.press(tea.KeyTab, tea.ModShift)
			if got := d.focused(); got != "auth" {
				t.Errorf("Shift+Tab from the key file page went to %q, want the auth key choice", got)
			}
		})
	}
}

func TestWizardStillNeedsAKeyFileToSave(t *testing.T) {
	_, ans, d := newWizard(t)
	fillIdentity(d)
	d.advanceTo("auth")
	d.choose(1) // Another key file…
	d.typeText("~/no-such-key")
	d.press(tea.KeyEnter)
	if d.focused() != "auth-path" {
		t.Fatalf("moved on from a key file that doesn't exist; focus is on %q", d.focused())
	}
	if d.form.State == huh.StateCompleted {
		t.Errorf("the account could be saved without a usable key file (save %v)", ans.Save)
	}
}

// DOP-19: signing with the auth key works again after the auth choice went
// to None and back, and every choice stays in view.
func TestWizardSigningChoicesFollowTheAuthChoiceBackAndForth(t *testing.T) {
	_, ans, d := newWizard(t)
	fillIdentity(d)
	d.advanceTo("auth")
	allShown := func() bool {
		view := d.view()
		for _, choice := range []string{"Sign with the auth key", "Generate a separate signing key", "Another key file…", "Don't sign"} {
			if !strings.Contains(view, choice) {
				return false
			}
		}
		return true
	}

	d.press(tea.KeyEnter) // Generate
	if d.focused() != "signing" || !allShown() {
		t.Fatalf("signing choices with a generated auth key:\n%s", d.view())
	}
	d.backTo("auth")
	d.choose(2) // None
	d.choose(1) // Generate a separate signing key
	if ans.Auth != keyNone || ans.Signing != keyGenerate {
		t.Fatalf("auth %q, signing %q; want none and generate", ans.Auth, ans.Signing)
	}
	d.backTo("auth")
	for range 2 {
		d.press(tea.KeyUp)
	}
	d.press(tea.KeyEnter) // Generate again
	if ans.Auth != keyGenerate || d.focused() != "signing" || !allShown() {
		t.Fatalf("back on Generate (auth %q, focus %q), signing doesn't show every choice:\n%s", ans.Auth, d.focused(), d.view())
	}
	d.press(tea.KeyUp)
	d.press(tea.KeyEnter) // Sign with the auth key
	if ans.Signing != keyWithAuth || d.focused() == "signing" {
		t.Errorf("signing with the auth key wasn't accepted: signing %q, focus %q", ans.Signing, d.focused())
	}
}

// DOP-20: the review shows names and paths exactly, underscores included.
func TestWizardReviewShowsUnderscoresAndStars(t *testing.T) {
	_, _, d := newWizard(t)
	for _, text := range []string{"work", "Jane *Star* Doe", "jane_doe@acme.com"} {
		d.typeText(text)
		d.press(tea.KeyEnter)
	}
	d.advanceTo("save")
	view := d.view()
	for _, want := range []string{"id_ed25519_work", "jane_doe@acme.com", "Jane *Star* Doe"} {
		if !strings.Contains(view, want) {
			t.Errorf("review doesn't show %q:\n%s", want, view)
		}
	}
}

// Changing the ID rewrites the auth page's description; the choices above
// the chosen one stay in view.
func TestWizardAuthChoicesStayVisibleAfterTheIDChanges(t *testing.T) {
	_, ans, d := newWizard(t)
	fillIdentity(d)
	d.advanceTo("auth")
	d.choose(2) // None, the last choice
	d.backTo("id")
	d.press('u', tea.ModCtrl)
	d.typeText("job")
	d.advanceTo("auth")
	if view := d.view(); ans.Auth != keyNone || !strings.Contains(view, "Generate a new key") || !strings.Contains(view, "Another key file…") {
		t.Errorf("auth %q; choices above the chosen one are hidden:\n%s", ans.Auth, view)
	}
}

func TestWizardIgnoresAHiddenPagesAnswer(t *testing.T) {
	s, ans, d := newWizard(t)
	fillIdentity(d)
	d.advanceTo("github-user")
	d.typeText("not a user!")
	d.backTo("hosts") // leaving a bad answer behind is allowed going back
	d.press('u', tea.ModCtrl)
	d.typeText("gitlab.com") // which hides the GitHub page
	d.advanceTo("save")
	d.press(tea.KeyEnter)
	if d.form.State != huh.StateCompleted {
		t.Fatalf("form state %v; saving was refused:\n%s", d.form.State, d.view())
	}
	acc := &accounts.Account{}
	s.newApp(s.Home).applyAnswers(ans, acc)
	if acc.GitHubUser != "" {
		t.Errorf("GitHub user = %q from a page that was hidden", acc.GitHubUser)
	}
}

func TestSaveChecksEveryShownPage(t *testing.T) {
	s := newSandbox(t)
	a := s.newApp(s.Home)
	acc := &accounts.Account{}
	none := func() bool { return false }
	cases := map[string]struct {
		ans  answers
		want string
	}{
		"bad host":             {answers{Hosts: "not a host!", Auth: keyNone, Signing: keyNone}, "Git hosts:"},
		"missing key file":     {answers{Hosts: "gitlab.com", Auth: keyPathEtc, AuthPath: "~/missing", Signing: keyNone}, "Auth key file:"},
		"signing without auth": {answers{Hosts: "gitlab.com", Auth: keyNone, Signing: keyWithAuth}, "Signing:"},
	}
	for name, c := range cases {
		err := a.checkAnswers(&c.ans, acc, none)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "Shift+Tab") {
			t.Errorf("%s: %v, want an error naming %q and how to go back", name, err, c.want)
		}
	}
	ok := answers{Hosts: "gitlab.com", Auth: keyNone, Signing: keyNone}
	if err := a.checkAnswers(&ok, acc, none); err != nil {
		t.Errorf("good answers: %v", err)
	}
}

func TestWizardWontGenerateOverAnExistingKey(t *testing.T) {
	s, ans, d := newWizard(t)
	s.Key("id_ed25519_work", "jane@acme.com", "")
	fillIdentity(d)
	d.advanceTo("auth")
	d.press(tea.KeyEnter) // Generate a new key
	if view := d.view(); ans.Auth != keyGenerate || d.focused() != "auth" || !strings.Contains(view, "~/.ssh/id_ed25519_work already exists") {
		t.Errorf("generating over an existing key wasn't refused (auth %q, focus %q):\n%s", ans.Auth, d.focused(), view)
	}
}

func TestWizardSaveRefusesAnswersThatNoLongerFit(t *testing.T) {
	_, ans, d := newWizard(t)
	fillIdentity(d)
	d.advanceTo("save")
	// No page path leads here with a bad answer, as moving on checks each
	// page; Save is the last line of defence.
	ans.Auth, ans.Signing = keyNone, keyWithAuth
	d.press(tea.KeyEnter)
	if view := d.view(); d.form.State == huh.StateCompleted || !strings.Contains(view, "there's no auth key to sign with") {
		t.Errorf("saved answers that don't fit (state %v):\n%s", d.form.State, view)
	}
}
