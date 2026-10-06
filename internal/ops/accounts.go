package ops

import (
	"fmt"
	"strings"

	"github.com/vehkiya/doppel/internal/accounts"
)

// AddRequest describes a new account.
type AddRequest struct {
	// Account holds the new account's ID and fields. It may come with keys
	// already set, such as an identity started from the global Git config,
	// which Keys then changes.
	Account *accounts.Account
	// Folders are bound to the account, as the user typed them.
	Folders []string
	// Default makes it the default account. The first account always is.
	Default bool
	Keys    KeyChanges
}

// Add adds an account.
func Add(ctx Context, list []*accounts.Account, req AddRequest) (*Change, error) {
	acc := req.Account
	if err := accounts.ValidateID(acc.ID); err != nil {
		return nil, err
	}
	if accounts.Find(list, acc.ID) != nil {
		return nil, fmt.Errorf("account %s already exists; change it with `doppel edit %s`", acc.ID, acc.ID)
	}
	if len(acc.Hosts) == 0 {
		acc.Hosts = []string{accounts.DefaultHost}
	}
	if err := acc.Validate(); err != nil {
		return nil, err
	}
	newKeys, err := applyKeys(ctx, acc, req.Keys)
	if err != nil {
		return nil, err
	}
	warnings, err := bindFolders(ctx, list, acc, req.Folders)
	if err != nil {
		return nil, err
	}
	list = append(list, acc)
	if len(list) == 1 || req.Default {
		accounts.SetDefault(list, acc)
	}
	return &Change{
		Accounts: list,
		NewKeys:  newKeys,
		Message:  "Added account " + acc.ID,
		Warnings: append(warnings, sharedKeyWarnings(ctx, list)...),
	}, nil
}

// EditRequest says what to change in an account. A nil field is kept.
type EditRequest struct {
	ID                      string
	Name, Email, GitHubUser *string
	Hosts                   []string  // replaces the hosts; nil keeps them
	Folders                 *[]string // replaces the folders, as the user typed them
	Default                 *bool
	Keys                    KeyChanges
}

// Empty reports whether the request changes nothing.
func (r EditRequest) Empty() bool {
	return r.Name == nil && r.Email == nil && r.GitHubUser == nil && r.Hosts == nil && r.Folders == nil &&
		r.Default == nil && !r.Keys.Any()
}

// Edit changes an account.
func Edit(ctx Context, list []*accounts.Account, req EditRequest) (*Change, error) {
	acc, err := Find(list, req.ID)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		acc.Name = *req.Name
	}
	if req.Email != nil {
		acc.Email = *req.Email
	}
	if req.GitHubUser != nil {
		acc.GitHubUser = *req.GitHubUser
	}
	if req.Hosts != nil {
		acc.Hosts = req.Hosts
	}
	var warnings []string
	if req.Folders != nil {
		acc.Folders = nil
		if warnings, err = bindFolders(ctx, list, acc, *req.Folders); err != nil {
			return nil, err
		}
	}
	if req.Default != nil {
		if *req.Default {
			accounts.SetDefault(list, acc)
		} else {
			acc.Default = false
		}
	}
	newKeys, err := applyKeys(ctx, acc, req.Keys)
	if err != nil {
		return nil, err
	}
	return &Change{
		Accounts: list,
		NewKeys:  newKeys,
		Message:  "Updated account " + acc.ID,
		Warnings: append(warnings, sharedKeyWarnings(ctx, list)...),
	}, nil
}

// RemoveRequest names the account to delete.
type RemoveRequest struct {
	ID string
	// NewDefault picks the account that becomes the default when the default
	// one is deleted, or nil for none. Without it there's no default left.
	NewDefault func(rest []*accounts.Account) (*accounts.Account, error)
}

// Remove deletes an account, its folder rules and its allowed_signers
// entry, after confirming. Key files are kept.
func Remove(ctx Context, list []*accounts.Account, req RemoveRequest) (*Change, error) {
	acc, err := Find(list, req.ID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Confirm(fmt.Sprintf("Delete account %s and its folder rules? Key files are kept.", acc.ID)); err != nil {
		return nil, err
	}
	var rest []*accounts.Account
	for _, other := range list {
		if other != acc {
			rest = append(rest, other)
		}
	}
	if acc.Default && len(rest) > 0 && req.NewDefault != nil {
		def, err := req.NewDefault(rest)
		if err != nil {
			return nil, err
		}
		accounts.SetDefault(rest, def)
	}
	ch := &Change{Accounts: rest, Removed: []*accounts.Account{acc}, Message: "Deleted account " + acc.ID}
	if acc.Default && len(rest) > 0 && accounts.Default(rest) == nil {
		ch.Notes = append(ch.Notes, "There's no default account now. Choose one with: doppel default <id>")
	}
	return ch, nil
}

// Rename changes an account's ID, keeping everything else.
func Rename(_ Context, list []*accounts.Account, oldID, newID string) (*Change, error) {
	if err := accounts.ValidateID(newID); err != nil {
		return nil, err
	}
	acc, err := Find(list, oldID)
	if err != nil {
		return nil, err
	}
	if accounts.Find(list, newID) != nil {
		return nil, fmt.Errorf("account %s already exists", newID)
	}
	acc.ID = newID
	return &Change{Accounts: list, Message: fmt.Sprintf("Renamed account %s to %s", oldID, newID)}, nil
}

// Bind binds folders to an account. A folder bound to another account
// moves after confirming.
func Bind(ctx Context, list []*accounts.Account, id string, folders []string) (*Change, error) {
	acc, err := Find(list, id)
	if err != nil {
		return nil, err
	}
	warnings, err := bindFolders(ctx, list, acc, folders)
	if err != nil {
		return nil, err
	}
	return &Change{
		Accounts: list,
		Message:  fmt.Sprintf("Bound to %s: %s", acc.ID, strings.Join(acc.Folders, ", ")),
		Warnings: warnings,
	}, nil
}

// Unbind removes folder rules, whichever accounts they belong to.
func Unbind(ctx Context, list []*accounts.Account, folders []string) (*Change, error) {
	var removed []string
	for _, input := range folders {
		folder, _, err := ctx.Env.NormalizeFolder(input, ctx.Cwd)
		if err != nil {
			return nil, err
		}
		owner := accounts.FolderOwner(ctx.Env, list, folder)
		if owner == nil {
			return nil, fmt.Errorf("%s isn't bound to any account", folder)
		}
		owner.RemoveFolder(ctx.Env, folder)
		removed = append(removed, fmt.Sprintf("%s (was %s)", folder, owner.ID))
	}
	return &Change{Accounts: list, Message: "Unbound " + strings.Join(removed, ", ")}, nil
}

// SetDefault makes the account named id the default, or with id "" leaves
// none.
func SetDefault(_ Context, list []*accounts.Account, id string) (*Change, error) {
	if id == "" {
		accounts.SetDefault(list, nil)
		return &Change{Accounts: list, Message: "Cleared the default account"}, nil
	}
	acc, err := Find(list, id)
	if err != nil {
		return nil, err
	}
	accounts.SetDefault(list, acc)
	return &Change{Accounts: list, Message: acc.ID + " is now the default account"}, nil
}

// bindFolders adds folders to acc. A folder bound to another account moves
// to acc after confirming. It warns about folders that don't exist yet.
func bindFolders(ctx Context, list []*accounts.Account, acc *accounts.Account, inputs []string) ([]string, error) {
	var warnings []string
	for _, input := range inputs {
		folder, exists, err := ctx.Env.NormalizeFolder(input, ctx.Cwd)
		if err != nil {
			return nil, err
		}
		if !exists {
			warnings = append(warnings, folder+" doesn't exist yet; the rule applies to repos created there later")
		}
		owner := accounts.FolderOwner(ctx.Env, list, folder)
		if owner == acc {
			continue
		}
		if owner != nil {
			if err := ctx.Confirm(fmt.Sprintf("%s is bound to %s. Move it to %s?", folder, owner.ID, acc.ID)); err != nil {
				return nil, err
			}
			owner.RemoveFolder(ctx.Env, folder)
		}
		acc.Folders = append(acc.Folders, folder)
	}
	return warnings, nil
}
