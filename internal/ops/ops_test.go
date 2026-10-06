package ops_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/ops"
	"github.com/vehkiya/doppel/internal/testenv"
)

// newContext answers every confirmation with answer, and records the questions.
func newContext(t *testing.T, answer error) (*testenv.Sandbox, ops.Context, *[]string) {
	t.Helper()
	s := testenv.New(t)
	var asked []string
	return s, ops.Context{Env: s.Env(), Cwd: s.Home, Confirm: func(q string) error {
		asked = append(asked, q)
		return answer
	}}, &asked
}

// save runs a change and fails the test if it doesn't save.
func save(t *testing.T, ctx ops.Context, ch *ops.Change, err error) *ops.Result {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	res, err := ops.Save(ctx, ch, ops.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func load(t *testing.T, ctx ops.Context) []*accounts.Account {
	t.Helper()
	list, err := accounts.Load(ctx.Env)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestAddEditRemove(t *testing.T) {
	s, ctx, _ := newContext(t, nil)
	s.Mkdir("work")
	s.Key("id_work", "jane@acme.com", "")

	auth := "~/.ssh/id_work"
	ch, err := ops.Add(ctx, nil, ops.AddRequest{
		Account: &accounts.Account{ID: "work", Name: "Jane", Email: "jane@acme.com"},
		Folders: []string{"~/work"}, Keys: ops.KeyChanges{Auth: &auth, SignWithAuth: true},
	})
	res := save(t, ctx, ch, err)
	if res.Message != "Added account work" || res.DryRun || len(res.Changes) == 0 {
		t.Errorf("result: %+v", res)
	}
	acc := accounts.Find(load(t, ctx), "work")
	if !acc.Default || acc.SigningKey != "~/.ssh/id_work.pub" || !acc.SignCommits || !slices.Equal(acc.Folders, []string{"~/work/"}) ||
		!slices.Equal(acc.Hosts, []string{accounts.DefaultHost}) {
		t.Errorf("added %+v", acc)
	}

	name := "Jane Doe"
	ch, err = ops.Edit(ctx, load(t, ctx), ops.EditRequest{ID: "work", Name: &name})
	if res := save(t, ctx, ch, err); res.Message != "Updated account work" {
		t.Errorf("edit result: %+v", res)
	}
	if acc := accounts.Find(load(t, ctx), "work"); acc.Name != "Jane Doe" || acc.AuthKey != "~/.ssh/id_work" {
		t.Errorf("edited %+v", acc)
	}
	if !(ops.EditRequest{ID: "work"}).Empty() {
		t.Error("an edit with no changes isn't Empty")
	}

	ch, err = ops.Remove(ctx, load(t, ctx), ops.RemoveRequest{ID: "work"})
	res = save(t, ctx, ch, err)
	if res.Message != "Deleted account work" || len(load(t, ctx)) != 0 {
		t.Errorf("remove: %+v", res)
	}
}

// Every way in validates the same: the wizard and the flags both build an
// AddRequest, so a bad field is refused before any key is looked at.
func TestAddValidatesBeforeKeys(t *testing.T) {
	_, ctx, _ := newContext(t, nil)
	missing := "~/.ssh/missing"
	_, err := ops.Add(ctx, nil, ops.AddRequest{
		Account: &accounts.Account{ID: "work", Name: "Jane", Email: "not an email"},
		Keys:    ops.KeyChanges{Auth: &missing},
	})
	if err == nil || !strings.Contains(err.Error(), "isn't a valid email address") {
		t.Errorf("Add = %v, want the email refused first", err)
	}
}

func TestBindAsksBeforeMovingAFolder(t *testing.T) {
	declined := errors.New("declined")
	s, ctx, _ := newContext(t, nil)
	s.Mkdir("work")
	for _, id := range []string{"personal", "work"} {
		ch, err := ops.Add(ctx, load(t, ctx), ops.AddRequest{Account: &accounts.Account{ID: id, Name: "Jane", Email: id + "@x.io"}})
		save(t, ctx, ch, err)
	}
	ch, err := ops.Bind(ctx, load(t, ctx), "personal", []string{"~/work", "~/later"})
	res := save(t, ctx, ch, err)
	if res.Message != "Bound to personal: ~/work/, ~/later/" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "~/later/ doesn't exist yet") {
		t.Errorf("bind: %+v", res)
	}

	var asked []string
	ctx.Confirm = func(q string) error {
		asked = append(asked, q)
		return declined
	}
	if _, err := ops.Bind(ctx, load(t, ctx), "work", []string{"~/work"}); !errors.Is(err, declined) {
		t.Errorf("Bind = %v, want the declined confirmation", err)
	}
	if len(asked) != 1 || asked[0] != "~/work/ is bound to personal. Move it to work?" {
		t.Errorf("asked %q", asked)
	}
}

func TestRemoveTheDefault(t *testing.T) {
	_, ctx, _ := newContext(t, nil)
	for _, id := range []string{"personal", "work"} {
		ch, err := ops.Add(ctx, load(t, ctx), ops.AddRequest{Account: &accounts.Account{ID: id, Name: "Jane", Email: id + "@x.io"}})
		save(t, ctx, ch, err)
	}

	// Without anyone to ask, no account takes over, and a note says so.
	ch, err := ops.Remove(ctx, load(t, ctx), ops.RemoveRequest{ID: "personal"})
	if err != nil || len(ch.Notes) != 1 || !strings.Contains(ch.Notes[0], "doppel default <id>") || accounts.Default(ch.Accounts) != nil {
		t.Errorf("Remove = %+v, %v", ch, err)
	}

	ch, err = ops.Remove(ctx, load(t, ctx), ops.RemoveRequest{ID: "personal", NewDefault: func(rest []*accounts.Account) (*accounts.Account, error) {
		return rest[0], nil
	}})
	save(t, ctx, ch, err)
	if acc := accounts.Find(load(t, ctx), "work"); !acc.Default {
		t.Error("work didn't become the default")
	}
}

func TestSaveDryRun(t *testing.T) {
	s, ctx, _ := newContext(t, nil)
	ch, err := ops.Add(ctx, nil, ops.AddRequest{
		Account: &accounts.Account{ID: "work", Name: "Jane", Email: "jane@acme.com"},
		Keys:    ops.KeyChanges{GenerateAuth: true, SignWithAuth: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Save(ctx, ch, ops.SaveOptions{}); !errors.Is(err, ops.ErrNeedsTerminal) {
		t.Errorf("Save without a way to generate keys = %v", err)
	}
	res, err := ops.Save(ctx, ch, ops.SaveOptions{DryRun: true, Lock: func() error { t.Error("a dry run took the lock"); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || len(res.NewKeys) != 1 || res.NewKeys[0].Path != s.Path(".ssh/id_ed25519_work") {
		t.Errorf("dry run: %+v", res)
	}
	if s.Exists(".config/doppel/accounts/work.gitconfig") || s.Exists(".ssh/id_ed25519_work") {
		t.Error("a dry run wrote files")
	}
	for _, c := range res.Changes {
		if strings.HasSuffix(c.Path, "allowed_signers") && !strings.Contains(string(c.New), "<the key generated at ~/.ssh/id_ed25519_work>") {
			t.Errorf("allowed_signers shows no placeholder:\n%s", c.New)
		}
	}
}

func TestExportKeys(t *testing.T) {
	s, ctx, _ := newContext(t, nil)
	s.Key("id_work", "jane@acme.com", "")
	s.Key("id_sign", "jane@acme.com", "")
	line := strings.TrimSpace(s.Read(".ssh/id_sign.pub"))

	same := &accounts.Account{ID: "work", AuthKey: "~/.ssh/id_work", SigningKey: "~/.ssh/id_work.pub"}
	got, err := ops.ExportKeys(ctx, same, false, false)
	if err != nil || len(got) != 1 || got[0].Purpose() != "Auth and signing key" || got[0].Line != strings.TrimSpace(s.Read(".ssh/id_work.pub")) {
		t.Errorf("one key for both uses: %+v, %v", got, err)
	}

	inline := &accounts.Account{ID: "work", AuthKey: "~/.ssh/id_work", SigningKey: keys.Ref(keys.LiteralPrefix + line)}
	got, err = ops.ExportKeys(ctx, inline, false, true)
	if err != nil || len(got) != 1 || got[0].Line != line || ops.PublicName(ctx, got[0].Key) != "inline ssh-ed25519 "+strings.Fields(line)[1][:16]+"…" {
		t.Errorf("an inline signing key: %+v, %v", got, err)
	}

	if _, err := ops.ExportKeys(ctx, &accounts.Account{ID: "bare"}, false, false); err == nil || !strings.Contains(err.Error(), "has no keys yet") {
		t.Errorf("an account without keys: %v", err)
	}
}

func TestWhoamiOutsideARepo(t *testing.T) {
	s, ctx, _ := newContext(t, nil)
	s.Mkdir("work")
	list := []*accounts.Account{
		{ID: "personal", Default: true},
		{ID: "work", Folders: []string{"~/work/"}},
	}
	id, err := ops.Whoami(ctx, list, s.Path("work/new-clone"))
	if err != nil || id.InRepo || id.Exists || id.NewRepoID != "work" || id.NewRepoRule != "folder ~/work/" {
		t.Errorf("Whoami = %+v, %v", id, err)
	}
	id, err = ops.Whoami(ctx, list, s.Path("elsewhere"))
	if err != nil || id.NewRepoID != "personal" || id.NewRepoRule != "default account" {
		t.Errorf("Whoami = %+v, %v", id, err)
	}
}
