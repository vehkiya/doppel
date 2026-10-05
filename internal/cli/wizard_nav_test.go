package cli

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/vehkiya/doppel/internal/accounts"
)

// formDriver feeds keys to a Huh form the way a terminal would, running the
// commands each key returns so focus moves as it does for real.
type formDriver struct {
	t    *testing.T
	form *huh.Form
}

func newFormDriver(t *testing.T, form *huh.Form) *formDriver {
	t.Helper()
	d := &formDriver{t: t, form: form}
	d.feed(form.Init())
	return d
}

func (d *formDriver) feed(cmd tea.Cmd) {
	queue := runCmd(cmd)
	for i := 0; len(queue) > 0 && i < 200; i++ {
		msg := queue[0]
		queue = queue[1:]
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

func (d *formDriver) typeText(text string) {
	d.feed(func() tea.Msg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)} })
}

func (d *formDriver) press(k tea.KeyType) {
	d.feed(func() tea.Msg { return tea.KeyMsg{Type: k} })
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
	s := newSandbox(t)
	a := s.newApp(s.Home)
	a.accessible = false
	ans, steps := a.wizardSteps(nil, &accounts.Account{Hosts: []string{accounts.DefaultHost}}, true)
	return s, ans, newFormDriver(t, wizardForm(steps))
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
		d.press(tea.KeyShiftTab)
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
		d.press(tea.KeyCtrlU) // clear the prefilled github.com
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
		d.press(tea.KeyShiftTab)
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
		if view := d.form.View(); !strings.Contains(view, "id_ed25519_work") {
			t.Fatalf("auth choices don't name the key after the ID:\n%s", view)
		}
		d.backTo("id")
		d.press(tea.KeyCtrlU)
		d.typeText("job")
		d.advanceTo("auth")
		if view := d.form.View(); !strings.Contains(view, "id_ed25519_job") || strings.Contains(view, "id_ed25519_work") {
			t.Errorf("auth choices still show the old ID:\n%s", view)
		}
	})

	t.Run("signing with the auth key is offered only when there is one", func(t *testing.T) {
		_, ans, d := newWizard(t)
		fillIdentity(d)
		d.advanceTo("auth")
		d.choose(2) // Generate, Another key file…, None
		if ans.Auth != keyNone {
			t.Fatalf("auth = %q, want none", ans.Auth)
		}
		if view := d.form.View(); d.focused() != "signing" || strings.Contains(view, "Sign with the auth key") {
			t.Errorf("signing page offers the auth key although there is none (focus %q):\n%s", d.focused(), view)
		}
		d.choose(2) // Generate a separate key, Another key file…, Don't sign
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
		d.press(tea.KeyCtrlU)
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
