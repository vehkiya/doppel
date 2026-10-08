package doctor

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vehkiya/doppel/internal/paths"
)

// ide checks local IDE configurations (JetBrains and VS Code / derivatives)
// and repository-level IDE files for settings that could conflict with
// doppel's Git and SSH configuration.
func (c *checker) ide() {
	prevFindings := len(c.findings)
	c.area = "IDE settings"

	c.checkRepoIDE()
	c.checkJetBrains()
	c.checkVSCode()

	// If no IDE settings were evaluated or found anywhere, restore the area
	if len(c.findings) == prevFindings {
		c.area = ""
	}
}

// checkRepoIDE checks IDE files (.vscode, .idea) in the current working directory
// and in bound account folders.
func (c *checker) checkRepoIDE() {
	env := c.Env
	checkedDirs := map[string]bool{}

	checkDir := func(dir string, isCwd bool) {
		clean := filepath.Clean(dir)
		if checkedDirs[clean] || !paths.FileExists(clean) {
			return
		}
		checkedDirs[clean] = true

		label := "Repo " + env.Shorten(clean)
		if isCwd {
			label = "Current repo"
		}

		// 1. Check .idea in repository
		ideaDir := filepath.Join(clean, ".idea")
		if paths.FileExists(ideaDir) {
			vcsXML := filepath.Join(ideaDir, "vcs.xml")
			if paths.FileExists(vcsXML) {
				if data, err := os.ReadFile(filepath.Clean(vcsXML)); err == nil { //nolint:gosec // IDE config file
					var cfg jetbrainsXML
					if err := xml.Unmarshal(data, &cfg); err == nil {
						if cfg.sshExecutable() == "IDEA_SSH" {
							c.problem("In Settings → Version Control → Git, set 'SSH executable' to 'Native' (see IDE.md)",
								"%s: .idea uses the built-in SSH executable, which ignores doppel's core.sshCommand", label)
						} else {
							c.ok("%s: .idea configured for native Git", label)
						}
					}
				}
			} else {
				c.ok("%s: .idea detected (native Git defaults)", label)
			}
		}

		// 2. Check .vscode in repository
		vscodeDir := filepath.Join(clean, ".vscode")
		if paths.FileExists(vscodeDir) {
			settingsPath := filepath.Join(vscodeDir, "settings.json")
			if paths.FileExists(settingsPath) {
				if data, err := os.ReadFile(filepath.Clean(settingsPath)); err == nil { //nolint:gosec // IDE config file
					cleaned := stripJSONComments(data)
					var settings map[string]any
					if err := json.Unmarshal(cleaned, &settings); err == nil {
						c.checkVSCodeMap(label+": .vscode", settings)
					}
				}
			} else {
				c.ok("%s: .vscode detected", label)
			}
		}
	}

	if c.Cwd != "" {
		checkDir(c.Cwd, true)
	}

	for _, acc := range c.Accounts {
		for _, f := range acc.Folders {
			realDir := env.Expand(f)
			checkDir(realDir, false)
		}
	}
}

// checkJetBrains scans JetBrains IDE options for SSH executable configuration.
func (c *checker) checkJetBrains() {
	env := c.Env
	jbDir := jetbrainsConfigDir(env)
	if !paths.FileExists(jbDir) {
		return
	}

	entries, err := os.ReadDir(jbDir)
	if err != nil {
		return
	}

	type ideInfo struct {
		folder  string
		builtIn bool
		hasXML  bool
	}

	families := map[string][]ideInfo{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		family := jetbrainsFamily(name)
		if family == "" {
			continue
		}

		info := ideInfo{folder: name}
		gitXMLPath := filepath.Join(jbDir, name, "options", "git.xml")
		if paths.FileExists(gitXMLPath) {
			info.hasXML = true
			if data, err := os.ReadFile(filepath.Clean(gitXMLPath)); err == nil { //nolint:gosec // IDE settings file
				var cfg jetbrainsXML
				if err := xml.Unmarshal(data, &cfg); err == nil {
					info.builtIn = (cfg.sshExecutable() == "IDEA_SSH")
				}
			}
		}

		families[family] = append(families[family], info)
	}

	// For each family, report problems first, or the newest version if all OK
	var familyNames []string
	for f := range families {
		familyNames = append(familyNames, f)
	}
	sort.Strings(familyNames)

	for _, f := range familyNames {
		ides := families[f]
		sort.Slice(ides, func(i, j int) bool {
			return ides[i].folder < ides[j].folder
		})

		hasProblem := false
		for _, item := range ides {
			if item.builtIn {
				hasProblem = true
				c.problem("In Settings → Version Control → Git, set 'SSH executable' to 'Native' (see IDE.md)",
					"%s uses the built-in SSH executable, which ignores doppel's core.sshCommand", friendlyJetBrainsName(item.folder))
			}
		}

		if !hasProblem && len(ides) > 0 {
			latest := ides[len(ides)-1]
			c.ok("%s uses native SSH executable", friendlyJetBrainsName(latest.folder))
		}
	}
}

// checkVSCode inspects global VS Code (and derivatives like VSCodium, Cursor) settings.
func (c *checker) checkVSCode() {
	env := c.Env
	configs := vscodeConfigFiles(env)

	for _, cfg := range configs {
		if !paths.FileExists(cfg.path) {
			continue
		}

		data, err := os.ReadFile(filepath.Clean(cfg.path)) //nolint:gosec // IDE settings file
		if err != nil {
			continue
		}

		cleaned := stripJSONComments(data)
		var settings map[string]any
		if err := json.Unmarshal(cleaned, &settings); err != nil {
			continue
		}

		c.checkVSCodeMap(cfg.name, settings)
	}
}

func (c *checker) checkVSCodeMap(label string, settings map[string]any) {
	hasHTTPSOrMulti := len(c.Accounts) > 1
	for _, acc := range c.Accounts {
		if acc.AllowsHTTPS() {
			hasHTTPSOrMulti = true
			break
		}
	}

	hasSigning := false
	for _, acc := range c.Accounts {
		if acc.SignCommits || acc.SigningKey != "" {
			hasSigning = true
			break
		}
	}

	reportedCred := false
	// 1. Check git.useBuiltinCredentialProvider
	if val, ok := settings["git.useBuiltinCredentialProvider"]; ok {
		if b, ok := val.(bool); ok {
			reportedCred = true
			if b && hasHTTPSOrMulti {
				c.warn("Set \"git.useBuiltinCredentialProvider\": false in settings.json (see IDE.md)",
					"%s: git.useBuiltinCredentialProvider is enabled, which can override account credentials on GitHub", label)
			} else if !b {
				c.ok("%s: built-in credential provider is disabled (native Git credentials active)", label)
			} else {
				c.ok("%s: native Git credentials active", label)
			}
		}
	}

	if !reportedCred {
		if hasHTTPSOrMulti {
			c.warn("Set \"git.useBuiltinCredentialProvider\": false in settings.json (see IDE.md)",
				"%s: git.useBuiltinCredentialProvider is not disabled (defaults to true), which may override account credentials on GitHub", label)
		} else {
			c.ok("%s: settings compatible with doppel", label)
		}
	}

	// 2. Check git.enableCommitSigning
	if val, ok := settings["git.enableCommitSigning"]; ok {
		if b, ok := val.(bool); ok {
			if !b && hasSigning {
				c.warn("Set \"git.enableCommitSigning\": true in settings.json (see IDE.md)",
					"%s: git.enableCommitSigning is false in settings.json", label)
			} else if b {
				c.ok("%s: commit signing is enabled in settings", label)
			}
		}
	}
}

type jetbrainsXML struct {
	Components []struct {
		Name    string `xml:"name,attr"`
		Options []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"option"`
	} `xml:"component"`
}

func (j *jetbrainsXML) sshExecutable() string {
	for _, c := range j.Components {
		for _, o := range c.Options {
			if o.Name == "SSH_EXECUTABLE" {
				return o.Value
			}
		}
	}
	return ""
}

func jetbrainsConfigDir(env *paths.Env) string {
	switch env.GOOS {
	case "darwin":
		return filepath.Join(env.Home, "Library", "Application Support", "JetBrains")
	case "windows":
		return filepath.Join(env.Home, "AppData", "Roaming", "JetBrains")
	default:
		return filepath.Join(env.ConfigDir, "JetBrains")
	}
}

type vscodeTarget struct {
	name string
	path string
}

func vscodeConfigFiles(env *paths.Env) []vscodeTarget {
	switch env.GOOS {
	case "darwin":
		return []vscodeTarget{
			{"VS Code", filepath.Join(env.Home, "Library", "Application Support", "Code", "User", "settings.json")},
			{"VSCodium", filepath.Join(env.Home, "Library", "Application Support", "VSCodium", "User", "settings.json")},
			{"Cursor", filepath.Join(env.Home, "Library", "Application Support", "Cursor", "User", "settings.json")},
		}
	case "windows":
		return []vscodeTarget{
			{"VS Code", filepath.Join(env.Home, "AppData", "Roaming", "Code", "User", "settings.json")},
			{"VSCodium", filepath.Join(env.Home, "AppData", "Roaming", "VSCodium", "User", "settings.json")},
			{"Cursor", filepath.Join(env.Home, "AppData", "Roaming", "Cursor", "User", "settings.json")},
		}
	default:
		return []vscodeTarget{
			{"VS Code", filepath.Join(env.ConfigDir, "Code", "User", "settings.json")},
			{"VSCodium", filepath.Join(env.ConfigDir, "VSCodium", "User", "settings.json")},
			{"Cursor", filepath.Join(env.ConfigDir, "Cursor", "User", "settings.json")},
		}
	}
}

func jetbrainsFamily(folder string) string {
	known := []string{
		"IntelliJIdea", "IdeaIC", "PyCharm", "PyCharmCE", "WebStorm", "GoLand",
		"CLion", "Rider", "RustRover", "PhpStorm", "RubyMine", "DataGrip", "Aqua",
	}
	for _, k := range known {
		if strings.HasPrefix(folder, k) {
			return k
		}
	}
	// Fall back to extracting leading letters
	var b strings.Builder
	for _, r := range folder {
		if r >= '0' && r <= '9' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func friendlyJetBrainsName(folder string) string {
	switch {
	case strings.HasPrefix(folder, "IntelliJIdea") || strings.HasPrefix(folder, "IdeaIC"):
		return "IntelliJ IDEA (" + folder + ")"
	case strings.HasPrefix(folder, "PyCharm"):
		return "PyCharm (" + folder + ")"
	case strings.HasPrefix(folder, "WebStorm"):
		return "WebStorm (" + folder + ")"
	case strings.HasPrefix(folder, "GoLand"):
		return "GoLand (" + folder + ")"
	case strings.HasPrefix(folder, "CLion"):
		return "CLion (" + folder + ")"
	case strings.HasPrefix(folder, "Rider"):
		return "Rider (" + folder + ")"
	case strings.HasPrefix(folder, "RustRover"):
		return "RustRover (" + folder + ")"
	default:
		return folder
	}
}

// stripJSONComments removes line (//) and block (/* ... */) comments,
// as well as trailing commas from JSONC data.
func stripJSONComments(data []byte) []byte {
	var out []byte
	inString := false
	inLineComment := false
	inBlockComment := false
	escaped := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		if inLineComment {
			if b == '\n' {
				inLineComment = false
				out = append(out, b)
			}
			continue
		}

		if inBlockComment {
			if b == '*' && i+1 < len(data) && data[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if inString {
			out = append(out, b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		// Not in comment or string.
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}

		if b == '/' && i+1 < len(data) {
			if data[i+1] == '/' {
				inLineComment = true
				i++
				continue
			} else if data[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}

		out = append(out, b)
	}

	// Remove trailing commas before } or ]
	var cleaned []byte
	for i := 0; i < len(out); i++ {
		if out[i] == ',' {
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				continue
			}
		}
		cleaned = append(cleaned, out[i])
	}

	return cleaned
}
