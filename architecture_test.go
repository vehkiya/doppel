package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/vehkiya/doppel"

// layers lists, for each package, the internal packages its code may import
// (tests aside). Dependencies point one way, from cli down to the leaves, so
// nothing imports cli and each layer only knows the ones below it. AGENTS.md
// §2.0 explains the layout; change both together.
var layers = map[string][]string{
	".":   {"cli"},
	"cli": {"accounts", "doctor", "git", "hosts", "keys", "ops", "paths", "proc", "store", "tui", "ui", "update", "version"},

	// What cli does, as structured results cli only renders.
	"ops":    {"accounts", "git", "github", "hosts", "keys", "paths", "plan", "store"},
	"doctor": {"accounts", "git", "hosts", "keys", "paths", "plan", "store"},
	// The browser only reads accounts and picks an action.
	"tui": {"accounts", "paths", "ui"},

	"store":    {"accounts", "git", "keys", "paths", "plan"},
	"accounts": {"git", "keys", "paths"},
	"hosts":    {"git", "github"},

	// Leaves, apart from the shared helpers atomicfile and proc.
	"plan":       {"atomicfile"},
	"update":     {"atomicfile"},
	"ui":         {"plan"},
	"git":        {"proc"},
	"keys":       {"proc"},
	"github":     {"proc"},
	"paths":      {},
	"proc":       {},
	"atomicfile": {},
	"version":    {},

	// Only tests use it.
	"testenv": {"git", "keys", "paths"},
}

// TestDependenciesPointOneWay checks every package's imports against layers,
// so a shortcut through the layers fails CI rather than creeping in.
func TestDependenciesPointOneWay(t *testing.T) {
	dirs, err := filepath.Glob("internal/*")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{".": true}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			found[filepath.Base(dir)] = true
		}
	}
	for pkg := range found {
		if _, ok := layers[pkg]; !ok {
			t.Errorf("internal/%s isn't in layers: add it, with the packages it may import", pkg)
		}
	}
	for pkg := range layers {
		if !found[pkg] {
			t.Errorf("layers lists %s, which doesn't exist", pkg)
		}
	}

	for pkg := range found {
		dir := "."
		if pkg != "." {
			dir = filepath.Join("internal", pkg)
		}
		for _, imp := range internalImports(t, dir) {
			if !slices.Contains(layers[pkg], imp) {
				t.Errorf("%s imports %s, which it may not (allowed: %s)", pkg, imp, strings.Join(layers[pkg], ", "))
			}
		}
	}
}

// internalImports lists the doppel packages the non-test Go files in dir
// import, by their name under internal/.
func internalImports(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if name, ok := strings.CutPrefix(path, module+"/internal/"); ok {
				seen[name] = true
			} else if path == module {
				seen["."] = true
			}
		}
	}
	var list []string
	for name := range seen {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}
