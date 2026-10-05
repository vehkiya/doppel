package accounts

import (
	"strings"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/paths"
)

// FromGlobal reads the identity in the user's global Git config, for a first
// account to start from: name and email, an SSH signing setup, and an auth
// key named with -i in core.sshCommand. It reads the global files
// themselves, so doppel's own accounts never show up here. ok is false when
// there's no name and email to start from.
func FromGlobal(env *paths.Env) (acc *Account, source string, ok bool) {
	last := map[string]string{}
	for _, f := range env.GlobalConfigFiles() {
		values, err := git.ReadConfigFile(f)
		if err != nil {
			continue
		}
		for _, key := range []string{KeyName, KeyEmail, KeySigningKey, KeyGPGFormat, KeyCommitSign, KeyTagSign, KeySSHCommand} {
			if v := values[strings.ToLower(key)]; len(v) > 0 {
				last[key] = v[len(v)-1]
				if key == KeyName || key == KeyEmail {
					source = f
				}
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
