package accounts

import (
	"strings"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/paths"
)

// FromGlobal reads the identity in the user's global Git config, for a first
// account to start from: name and email, an SSH signing setup, and an auth
// key named with -i in core.sshCommand. It reads the global files and the
// files they include, as Git does, skipping doppel's own, so its accounts
// never show up here. source is the file the name and email came from. ok
// is false when there's no name and email to start from.
func FromGlobal(env *paths.Env) (acc *Account, source string, ok bool) {
	wanted := map[string]string{}
	for _, key := range []string{KeyName, KeyEmail, KeySigningKey, KeyGPGFormat, KeyCommitSign, KeyTagSign, KeySSHCommand} {
		wanted[strings.ToLower(key)] = key
	}
	last := map[string]string{}
	for _, f := range env.GlobalConfigFiles() {
		entries, err := git.ReadConfigFileIncludes(f, env.InDoppelDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			key, ok := wanted[e.Key]
			if !ok {
				continue
			}
			last[key] = e.Value
			if key == KeyName || key == KeyEmail {
				source = e.Origin
			}
		}
	}
	if last[KeyName] == "" || last[KeyEmail] == "" {
		return nil, "", false
	}
	acc = &Account{Name: last[KeyName], Email: last[KeyEmail], Hosts: []string{DefaultHost}}
	if strings.EqualFold(last[KeyGPGFormat], "ssh") && last[KeySigningKey] != "" {
		acc.SigningKey = shortenKey(env, last[KeySigningKey])
		acc.SignCommits = ParseBool(last[KeyCommitSign])
		acc.SignTags = ParseBool(last[KeyTagSign])
	}
	if key := identityFile(last[KeySSHCommand]); key != "" {
		acc.AuthKey = shortenKey(env, key)
	}
	return acc, source, true
}

// identityFile finds the key an ssh command line names with -i.
func identityFile(command string) string {
	fields := strings.Fields(command)
	for i, f := range fields {
		switch {
		case f == "-i" && i+1 < len(fields):
			return strings.Trim(fields[i+1], `'"`)
		case strings.HasPrefix(f, "-i") && len(f) > 2:
			return strings.Trim(f[2:], `'"`)
		}
	}
	return ""
}

func shortenKey(env *paths.Env, key string) string {
	if strings.HasPrefix(key, "key::") {
		return key
	}
	return env.Shorten(env.Expand(key))
}
