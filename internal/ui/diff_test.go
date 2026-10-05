package ui

import (
	"bytes"
	"testing"

	"github.com/vehkiya/doppel/internal/plan"
)

func TestWriteDiff(t *testing.T) {
	old := []byte("a\nb\nc\nd\ne\nf\ng\nh\n")
	updated := []byte("a\nB\nc\nd\ne\nf\ng\nh\ni\n")
	var buf bytes.Buffer
	WriteDiff(&buf, "~/f", plan.Change{Old: old, New: updated, Existed: true, Exists: true})
	want := `--- ~/f
+++ ~/f
@@ -1,4 +1,4 @@
 a
-b
+B
 c
 d
@@ -7,2 +7,3 @@
 g
 h
+i
`
	if got := buf.String(); got != want {
		t.Errorf("writeDiff:\n%s\nwant:\n%s", got, want)
	}

	buf.Reset()
	WriteDiff(&buf, "~/new", plan.Change{New: []byte("x\n"), Exists: true})
	if got, want := buf.String(), "new file ~/new\n@@ -0,0 +1,1 @@\n+x\n"; got != want {
		t.Errorf("writeDiff(new file) = %q, want %q", got, want)
	}
}
