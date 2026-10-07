package accounts

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vehkiya/doppel/internal/git"
	"github.com/vehkiya/doppel/internal/keys"
	"github.com/vehkiya/doppel/internal/paths"
)

// IncludeCleanup identifies an includeIf directive in a Git config file to
// remove when its account is imported.
type IncludeCleanup struct {
	ConfigFile string // e.g. ~/.gitconfig
	Key        string // e.g. "includeif.gitdir:~/work/.path"
	Subsection string // e.g. "gitdir:~/work/"
	Path       string // e.g. "~/.gitconfig-work"
}

// DiscoveredAccount is an account found in the user's Git configuration.
type DiscoveredAccount struct {
	Account  *Account
	Source   string           // the file the account's identity came from
	Cleanups []IncludeCleanup // includeIf directives to remove if imported
}

// Discovered holds every account found in the user's Git configuration.
type Discovered struct {
	Accounts []*DiscoveredAccount
}

// AsAccounts returns the slice of *Account from the discovered accounts.
func (d *Discovered) AsAccounts() []*Account {
	if d == nil {
		return nil
	}
	out := make([]*Account, len(d.Accounts))
	for i, da := range d.Accounts {
		out[i] = da.Account
	}
	return out
}

// AllCleanups returns all IncludeCleanups across all discovered accounts.
func (d *Discovered) AllCleanups() []IncludeCleanup {
	if d == nil {
		return nil
	}
	var cleanups []IncludeCleanup
	for _, da := range d.Accounts {
		cleanups = append(cleanups, da.Cleanups...)
	}
	return cleanups
}

// FromGlobal reads the identity in the user's global Git config, for a first
// account to start from: name and email, an SSH signing setup, and an auth
// key named with -i in core.sshCommand. It reads the global files and the
// files they include, as Git does, skipping doppel's own, so its accounts
// never show up here. source is the file the name and email came from. ok
// is false when there's no name and email to start from.
func FromGlobal(env *paths.Env) (acc *Account, source string, ok bool) {
	wanted := map[string]string{}
	for _, key := range IdentityKeys() {
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
		acc.SigningKey = keys.Ref(last[KeySigningKey]).Map(env.Expand).Map(env.Shorten)
		acc.SignCommits = ParseBool(last[KeyCommitSign])
		acc.SignTags = ParseBool(last[KeyTagSign])
	}
	if key := identityFile(last[KeySSHCommand]); key != "" {
		acc.AuthKey = keys.Ref(key).Map(env.Expand).Map(env.Shorten)
	}
	return acc, source, true
}

// Discover inspects the user's global Git configuration for accounts: the
// top-level [user] base identity (if any), and conditional identities in
// [includeIf "gitdir:..."] (and gitdir/i:) directives pointing to files
// defining an identity (user.name and/or user.email).
func Discover(env *paths.Env) (*Discovered, error) {
	var baseAcc *Account
	var baseSource string
	baseFound, src, ok := FromGlobal(env)
	if ok {
		baseAcc = baseFound
		baseSource = src
	}

	byResolvedPath := map[string]*DiscoveredAccount{}
	var condList []*DiscoveredAccount

	for _, f := range env.GlobalConfigFiles() {
		if !paths.FileExists(f) {
			continue
		}
		out, err := git.RunAlone("config", "--file", f, "--list", "--null")
		if err != nil {
			continue
		}
		entries := git.ParseConfigList(out, false)
		for _, e := range entries {
			lowKey := strings.ToLower(e.Key)
			if !strings.HasPrefix(lowKey, "includeif.") || !strings.HasSuffix(lowKey, ".path") {
				continue
			}
			sub := e.Key[len("includeif.") : len(e.Key)-len(".path")]
			var pattern string
			lowSub := strings.ToLower(sub)
			switch {
			case strings.HasPrefix(lowSub, "gitdir:"):
				pattern = sub[len("gitdir:"):]
			case strings.HasPrefix(lowSub, "gitdir/i:"):
				pattern = sub[len("gitdir/i:"):]
			default:
				continue
			}

			folder, _, err := env.NormalizeFolder(pattern, env.Home)
			if err != nil {
				continue
			}

			incPath := git.IncludePath(f, e.Value)
			if incPath == "" || env.InDoppelDir(incPath) {
				continue
			}
			realIncPath, exists := paths.ResolveExisting(filepath.Clean(incPath))
			if !exists {
				continue
			}

			cleanup := IncludeCleanup{
				ConfigFile: f,
				Key:        e.Key,
				Subsection: sub,
				Path:       e.Value,
			}

			if existing, ok := byResolvedPath[realIncPath]; ok {
				if !slices.Contains(existing.Account.Folders, folder) {
					existing.Account.Folders = append(existing.Account.Folders, folder)
				}
				existing.Cleanups = append(existing.Cleanups, cleanup)
				continue
			}

			incEntries, err := git.ReadConfigFileIncludes(incPath, env.InDoppelDir)
			if err != nil {
				continue
			}
			incSettings := map[string]string{}
			for _, ie := range incEntries {
				incSettings[strings.ToLower(ie.Key)] = ie.Value
			}

			name := incSettings[strings.ToLower(KeyName)]
			email := incSettings[strings.ToLower(KeyEmail)]
			if name == "" && email == "" {
				continue
			}
			if name == "" && baseAcc != nil {
				name = baseAcc.Name
			}
			if email == "" && baseAcc != nil {
				email = baseAcc.Email
			}
			if name == "" || email == "" {
				continue
			}

			var authKey keys.Ref
			if k := identityFile(incSettings[strings.ToLower(KeySSHCommand)]); k != "" {
				authKey = keys.Ref(k).Map(env.Expand).Map(env.Shorten)
			}
			var signingKey keys.Ref
			var signCommits, signTags bool
			sigKeyVal := incSettings[strings.ToLower(KeySigningKey)]
			formatVal := incSettings[strings.ToLower(KeyGPGFormat)]
			if formatVal == "" && baseAcc != nil && baseAcc.SigningKey != "" {
				formatVal = "ssh"
			}
			if sigKeyVal != "" && (strings.EqualFold(formatVal, "ssh") || strings.HasPrefix(sigKeyVal, "ssh-") || strings.HasPrefix(sigKeyVal, "key::") || strings.HasSuffix(sigKeyVal, ".pub")) {
				ref := keys.Ref(sigKeyVal).Map(env.Expand)
				if _, err := keys.ReadPublic(ref); err == nil {
					signingKey = ref.Map(env.Shorten)
					signCommits = ParseBool(incSettings[strings.ToLower(KeyCommitSign)])
					signTags = ParseBool(incSettings[strings.ToLower(KeyTagSign)])
				}
			}

			acc := &Account{
				Name:        name,
				Email:       email,
				Hosts:       []string{DefaultHost},
				Folders:     []string{folder},
				AuthKey:     authKey,
				SigningKey:  signingKey,
				SignCommits: signCommits,
				SignTags:    signTags,
			}
			da := &DiscoveredAccount{
				Account:  acc,
				Source:   incPath,
				Cleanups: []IncludeCleanup{cleanup},
			}
			byResolvedPath[realIncPath] = da
			condList = append(condList, da)
		}
	}

	usedIDs := map[string]bool{}
	var result []*DiscoveredAccount

	if baseAcc != nil {
		baseID := "personal"
		if usedIDs[baseID] {
			baseID = "default"
		}
		baseID = disambiguateID(baseID, usedIDs)
		usedIDs[baseID] = true
		baseAcc.ID = baseID
		baseAcc.Default = true
		result = append(result, &DiscoveredAccount{
			Account: baseAcc,
			Source:  baseSource,
		})
	}

	for _, da := range condList {
		id := deriveID(da.Source, da.Account.Folders, usedIDs)
		da.Account.ID = id
		usedIDs[id] = true
		result = append(result, da)
	}

	hasDefault := false
	for _, da := range result {
		if da.Account.Default {
			hasDefault = true
			break
		}
	}
	if !hasDefault && len(result) > 0 {
		result[0].Account.Default = true
	}

	return &Discovered{Accounts: result}, nil
}

func deriveID(filePath string, folders []string, used map[string]bool) string {
	name := filepath.Base(filePath)
	clean := cleanBaseName(name)
	if (clean == "" || isGenericName(clean)) && len(folders) > 0 {
		clean = cleanBaseName(filepath.Base(filepath.Clean(folders[0])))
	}
	if clean == "" || isGenericName(clean) {
		parent := filepath.Base(filepath.Dir(filePath))
		if !isGenericName(parent) {
			clean = cleanBaseName(parent)
		}
	}
	id := sanitizeID(clean)
	if id == "" {
		id = "account"
	}
	return disambiguateID(id, used)
}

func cleanBaseName(s string) string {
	for _, ext := range []string{".gitconfig", ".config", ".inc"} {
		if strings.HasSuffix(strings.ToLower(s), ext) {
			s = s[:len(s)-len(ext)]
		}
	}
	low := strings.ToLower(s)
	for _, pfx := range []string{".gitconfig-", ".gitconfig.", ".gitconfig_", "gitconfig-", "gitconfig.", "gitconfig_", ".gitconfig", "gitconfig"} {
		if strings.HasPrefix(low, pfx) {
			s = s[len(pfx):]
			break
		}
	}
	return strings.Trim(s, ".-_")
}

func isGenericName(s string) bool {
	low := strings.ToLower(strings.Trim(s, ".-_"))
	switch low {
	case "", "git", "config", "gitconfig", "etc", "home", ".config", "projects", "repos", "repo":
		return true
	}
	return false
}

func sanitizeID(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	res := strings.TrimRight(b.String(), "-")
	if len(res) > 32 {
		res = strings.TrimRight(res[:32], "-")
	}
	return res
}

func disambiguateID(id string, used map[string]bool) string {
	if !used[id] {
		return id
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("-%d", i)
		base := id
		if len(base)+len(suffix) > 32 {
			base = strings.TrimRight(base[:32-len(suffix)], "-")
		}
		candidate := base + suffix
		if !used[candidate] {
			return candidate
		}
	}
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
