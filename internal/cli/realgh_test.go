package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The tests must never reach the real gh, and so the developer's real
// GitHub accounts. A gh that logs its calls, put first on PATH before the
// sandbox exists, stands in for the real one: the sandbox's own fake has to
// shadow it, and gh's environment settings have to be gone.
func TestTestsNeverCallTheRealGH(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$@\" >> '" + calls + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0700); err != nil { //nolint:gosec // the stand-in has to be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "real-token")
	t.Setenv("GITHUB_TOKEN", "real-token")
	t.Setenv("GH_HOST", "example.com")

	s := newSandbox(t)
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_HOST"} {
		if v := os.Getenv(name); v != "" {
			t.Errorf("%s = %q inside the sandbox", name, v)
		}
	}

	// Commands that look at every host an account uses, which asks gh whether
	// an unfamiliar host is a GitHub Enterprise Server.
	s.addAccount("work", "jane@acme.com", "--host", "codeberg.org", "--host", "gitlab.acme.com", "--github-user", "jane")
	s.run("doctor")
	s.run("export", "work", "--no-copy")
	s.run("upload", "work")

	if data, err := os.ReadFile(calls); err == nil { //nolint:gosec // the stand-in gh's log, in a temp directory
		t.Errorf("the real gh was called:\n%s", data)
	}
}
