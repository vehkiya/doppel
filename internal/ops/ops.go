// Package ops carries out doppel's changes to accounts. Each operation takes
// the loaded accounts and a typed request, which a command builds from its
// flags and a wizard from its answers, and works out a Change; Save writes
// it and returns a Result saying what happened. Commands, wizards and the
// browser all go through here, so they can't drift apart, and how a result
// looks is left to the caller.
package ops

import (
	"errors"
	"fmt"

	"github.com/vehkiya/doppel/internal/accounts"
	"github.com/vehkiya/doppel/internal/hosts"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
	"github.com/vehkiya/doppel/internal/plan"
	"github.com/vehkiya/doppel/internal/store"
)

// Context is what every operation needs from the command running it.
type Context struct {
	Env *paths.Env
	// Cwd is where relative folders and key paths are read from.
	Cwd string
	// Confirm asks a yes/no question, such as whether to move a folder bound
	// to another account. An error stops the operation; it should answer
	// yes on its own for --yes, and fail without a terminal to ask on.
	Confirm func(question string) error
	// GitHub tells which hosts are GitHub, asking gh once per host.
	GitHub *hosts.GitHub
	// Keychain reports whether passphrases can be kept in the macOS
	// Keychain: this is a Mac, and its ssh is Apple's. nil means no.
	Keychain func() bool
}

func (ctx Context) keychain() bool { return ctx.Keychain != nil && ctx.Keychain() }

// Change is an operation worked out against the loaded accounts, ready for
// Save.
type Change struct {
	// Accounts is every account once the change is made.
	Accounts []*accounts.Account
	// Removed lists the accounts the change deletes. No other account file
	// is ever deleted, apart from the old file of a renamed account.
	Removed []*accounts.Account
	// NewKeys are keys to generate before saving.
	NewKeys []NewKey
	// Message says what the change does, for when it's done: "Added account work".
	Message string
	// Warnings are worth reading before the change is saved.
	Warnings []string
	// Notes are worth reading once it's saved.
	Notes []string
}

// Result is what Save did.
type Result struct {
	Message string
	// DryRun is true when nothing was written: Changes and NewKeys are then
	// what a real run would write and generate.
	DryRun bool
	// Changes are the files written, or that would be.
	Changes []plan.Change
	// NewKeys are the keys generated, or that would be.
	NewKeys  []NewKey
	Warnings []string
	Notes    []string
}

// SaveOptions says how to save a change.
type SaveOptions struct {
	// DryRun works out the changes without writing anything or generating
	// keys.
	DryRun bool
	// Generate creates a new key, asking for its passphrase on the terminal.
	// nil means there's no terminal, so a change that needs a key fails.
	Generate func(NewKey) error
	// Lock takes the write lock before anything is written, unless the
	// caller already holds it. A dry run doesn't take it.
	Lock func() error
}

// ErrNeedsTerminal is returned when a change needs a new key and nothing
// can ask for its passphrase.
var ErrNeedsTerminal = errors.New("generating a key needs a terminal, so ssh-keygen can ask for a passphrase. Generate it with ssh-keygen yourself and pass it with --auth-key or --signing-key")

// Save validates a change, generates its new keys, and writes it through
// store.Save and a plan, or for a dry run works out what it would write.
//
// The change is staged with placeholders for its new keys before any key is
// generated, so a change that can't be saved (an account's signing key that
// can't be read, say) fails before the user picks a passphrase, and leaves
// no key behind to trip up the next try.
func Save(ctx Context, ch *Change, opts SaveOptions) (*Result, error) {
	if err := accounts.ValidateAll(ctx.Env, ch.Accounts); err != nil {
		return nil, err
	}
	if opts.DryRun {
		return stage(ctx, ch, placeholders(ctx.Env, ch.NewKeys), preview)
	}
	if len(ch.NewKeys) > 0 {
		if _, err := stage(ctx, ch, placeholders(ctx.Env, ch.NewKeys), check); err != nil {
			return nil, err
		}
	}
	if len(ch.NewKeys) > 0 && opts.Generate == nil {
		return nil, ErrNeedsTerminal
	}
	for i, k := range ch.NewKeys {
		if err := opts.Generate(k); err != nil {
			return nil, keptKeys(ctx, ch.NewKeys[:i], err)
		}
	}
	if opts.Lock != nil {
		if err := opts.Lock(); err != nil {
			return nil, keptKeys(ctx, ch.NewKeys, err)
		}
	}
	res, err := stage(ctx, ch, nil, apply)
	if err != nil {
		return nil, keptKeys(ctx, ch.NewKeys, err)
	}
	return res, nil
}

// finish is what stage does with the plan it staged.
type finish int

const (
	apply   finish = iota // write it
	preview               // work out its changes, for a dry run
	check                 // only see that it stages
)

// stage stages a change, reading public keys with publicKey (nil for the
// key files), and finishes the plan as asked.
func stage(ctx Context, ch *Change, publicKey func(keys.Ref) (string, error), then finish) (*Result, error) {
	p := plan.New(ctx.Env.StagingDir())
	defer p.Close()
	if err := store.Save(ctx.Env, p, ch.Accounts, store.Options{Removed: ch.Removed, PublicKey: publicKey}); err != nil {
		return nil, err
	}
	res := &Result{Message: ch.Message, DryRun: then == preview, NewKeys: ch.NewKeys, Warnings: ch.Warnings, Notes: ch.Notes}
	var err error
	switch then {
	case check:
		return nil, nil
	case preview:
		res.Changes, err = p.Changes()
	default:
		res.Changes, err = p.Apply()
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

// keptKeys adds to err, from a save that failed after generating keys, that
// the keys were kept (doppel never deletes keys) and how to use them on the
// next try.
func keptKeys(ctx Context, generated []NewKey, err error) error {
	for _, k := range generated {
		flag := "--auth-key"
		if k.Purpose == "signing key" {
			flag = "--signing-key"
		}
		short := ctx.Env.Shorten(k.Path)
		err = fmt.Errorf("%w. The new %s %s was kept: use it with %s %s", err, k.Purpose, short, flag, short)
	}
	return err
}

// placeholders stands in for keys a dry run would generate, so the
// allowed_signers diff can show where they'd go.
func placeholders(env *paths.Env, newKeys []NewKey) func(keys.Ref) (string, error) {
	return func(key keys.Ref) (string, error) {
		for _, k := range newKeys {
			if key.SameKey(keys.Ref(k.Path)) {
				return "ssh-ed25519 <the key generated at " + env.Shorten(k.Path) + ">", nil
			}
		}
		return keys.ReadPublic(key)
	}
}

// Find returns the account named id, or an error saying there's none.
func Find(list []*accounts.Account, id string) (*accounts.Account, error) {
	if acc := accounts.Find(list, id); acc != nil {
		return acc, nil
	}
	return nil, fmt.Errorf("no account named %s", id)
}

// Uninstall takes doppel's include out of the global Git config and its
// block out of allowed_signers, leaving the account files and keys. The
// Result has no Changes when there was nothing to remove.
func Uninstall(ctx Context, opts SaveOptions) (*Result, error) {
	if !opts.DryRun && opts.Lock != nil {
		if err := opts.Lock(); err != nil {
			return nil, err
		}
	}
	p := plan.New(ctx.Env.StagingDir())
	defer p.Close()
	removedInclude, err := store.RemoveInclude(ctx.Env, p)
	if err != nil {
		return nil, err
	}
	removedSigners, err := store.RemoveSigners(ctx.Env, p)
	if err != nil {
		return nil, err
	}
	res := &Result{DryRun: opts.DryRun}
	if !removedInclude && !removedSigners {
		return res, nil
	}
	res.Message = "Removed doppel's include from " + ctx.Env.Shorten(ctx.Env.GlobalConfigPath())
	if removedSigners {
		res.Message += " and its keys from allowed_signers"
	}
	res.Notes = []string{fmt.Sprintf("Account files are still in %s. Any doppel command that changes accounts adds the include back.",
		ctx.Env.Shorten(ctx.Env.AccountsDir()))}
	if opts.DryRun {
		res.Changes, err = p.Changes()
	} else {
		res.Changes, err = p.Apply()
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}
