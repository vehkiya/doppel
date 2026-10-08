package doctor

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"

	"github.com/vehkiya/doppel/internal/paths"
)

// ide checks local IDE configurations (JetBrains and VS Code / derivatives)
// for settings that could conflict with doppel's Git and SSH configuration.
func (c *checker) ide() {
	prevFindings := len(c.findings)
	c.area = "IDE settings"

	c.checkJetBrains()
	c.checkVSCode()

	// If no IDE settings were evaluated or found, restore the area
	if len(c.findings) == prevFindings {
		c.area = ""
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

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		gitXMLPath := filepath.Join(jbDir, entry.Name(), "options", "git.xml")
		if !paths.FileExists(gitXMLPath) {
			continue
		}

		data, err := os.ReadFile(filepath.Clean(gitXMLPath)) //nolint:gosec // IDE settings file
		if err != nil {
			continue
		}

		var cfg jetbrainsXML
		if err := xml.Unmarshal(data, &cfg); err != nil {
			continue
		}

		sshExec := cfg.sshExecutable()
		ideName := friendlyJetBrainsName(entry.Name())
		if sshExec == "IDEA_SSH" {
			c.problem("In Settings → Version Control → Git, set 'SSH executable' to 'Native' (see IDE.md)",
				"%s uses the built-in SSH executable, which ignores doppel's core.sshCommand", ideName)
		} else {
			c.ok("%s uses native SSH executable", ideName)
		}
	}

	// Check per-project .idea/vcs.xml in account folders
	for _, acc := range c.Accounts {
		for _, f := range acc.Folders {
			realDir := env.Expand(f)
			vcsXML := filepath.Join(realDir, ".idea", "vcs.xml")
			if !paths.FileExists(vcsXML) {
				continue
			}
			data, err := os.ReadFile(filepath.Clean(vcsXML)) //nolint:gosec // IDE settings file
			if err != nil {
				continue
			}
			var cfg jetbrainsXML
			if err := xml.Unmarshal(data, &cfg); err != nil {
				continue
			}
			if cfg.sshExecutable() == "IDEA_SSH" {
				c.problem("In Settings → Version Control → Git, set 'SSH executable' to 'Native' (see IDE.md)",
					"%s/.idea uses the built-in SSH executable, which ignores doppel's core.sshCommand", env.Shorten(realDir))
			}
		}
	}
}

// checkVSCode inspects VS Code (and derivatives like VSCodium, Cursor) settings.
func (c *checker) checkVSCode() {
	env := c.Env
	configs := vscodeConfigFiles(env)

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

		// 1. Check git.useBuiltinCredentialProvider
		if val, ok := settings["git.useBuiltinCredentialProvider"]; ok {
			if b, ok := val.(bool); ok {
				if b && hasHTTPSOrMulti {
					c.warn("Set \"git.useBuiltinCredentialProvider\": false in settings.json (see IDE.md)",
						"%s: git.useBuiltinCredentialProvider is enabled, which can override account credentials on GitHub", cfg.name)
				} else if !b {
					c.ok("%s: built-in credential provider is disabled (native Git credentials active)", cfg.name)
				}
			}
		}

		// 2. Check git.enableCommitSigning
		if val, ok := settings["git.enableCommitSigning"]; ok {
			if b, ok := val.(bool); ok {
				if !b && hasSigning {
					c.warn("Set \"git.enableCommitSigning\": true in settings.json (see IDE.md)",
						"%s: git.enableCommitSigning is false in settings.json", cfg.name)
				} else if b {
					c.ok("%s: commit signing is enabled in settings", cfg.name)
				}
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

func friendlyJetBrainsName(folder string) string {
	switch {
	case strings.HasPrefix(folder, "IntelliJIdea"):
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
