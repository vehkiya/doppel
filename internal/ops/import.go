package ops

import (
	"errors"
	"fmt"

	"github.com/vehkiya/doppel/internal/accounts"
)

// ImportRequest describes accounts to import from Git configuration.
type ImportRequest struct {
	Discovered *accounts.Discovered
}

// Import imports discovered Git configuration into doppel accounts.
func Import(ctx Context, req ImportRequest) (*Change, error) {
	if req.Discovered == nil || len(req.Discovered.Accounts) == 0 {
		return nil, errors.New("no accounts to import")
	}

	list := make([]*accounts.Account, len(req.Discovered.Accounts))
	var cleanups []accounts.IncludeCleanup
	for i, da := range req.Discovered.Accounts {
		list[i] = da.Account
		cleanups = append(cleanups, da.Cleanups...)
	}

	if err := accounts.ValidateAll(ctx.Env, list); err != nil {
		return nil, err
	}

	hasDefault := false
	for _, a := range list {
		if a.Default {
			hasDefault = true
			break
		}
	}
	if !hasDefault && len(list) > 0 {
		list[0].Default = true
	}

	msg := fmt.Sprintf("Imported %d accounts", len(list))
	if len(list) == 1 {
		msg = "Imported 1 account"
	}

	return &Change{
		Accounts: list,
		Cleanups: cleanups,
		Message:  msg,
		Warnings: sharedKeyWarnings(ctx, list),
	}, nil
}
