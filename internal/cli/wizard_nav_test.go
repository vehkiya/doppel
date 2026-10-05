package cli

import (
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
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
