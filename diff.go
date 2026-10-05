package main

import (
	"fmt"
	"io"
	"strings"
)

const diffContext = 2

type diffOp struct {
	kind byte // ' ', '-' or '+'
	line string
}

// diffLines computes a line diff of a → b from their longest common
// subsequence. Config files are small, so the quadratic table is fine.
func diffLines(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

func splitLines(data []byte) []string {
	s := strings.TrimSuffix(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// writeDiff prints a unified diff of one planned change, labelled with name.
func writeDiff(w io.Writer, name string, c fileChange) {
	header := lineStyle('@')
	switch {
	case !c.Existed:
		_, _ = fmt.Fprintln(w, header.Render("new file "+name))
	case !c.Exists:
		_, _ = fmt.Fprintln(w, header.Render("delete "+name))
	default:
		_, _ = fmt.Fprintln(w, header.Render("--- "+name))
		_, _ = fmt.Fprintln(w, header.Render("+++ "+name))
	}

	ops := diffLines(splitLines(c.Old), splitLines(c.New))
	for start := 0; start < len(ops); {
		// Find the next change, then extend the hunk while changes are
		// close enough for their context to touch.
		first := start
		for first < len(ops) && ops[first].kind == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		last := first
		for k := first; k < len(ops); k++ {
			if ops[k].kind != ' ' {
				last = k
			} else if k-last > 2*diffContext {
				break
			}
		}
		from, to := max(first-diffContext, 0), min(last+diffContext+1, len(ops))

		oldLine, newLine := 1, 1
		for _, op := range ops[:from] {
			if op.kind != '+' {
				oldLine++
			}
			if op.kind != '-' {
				newLine++
			}
		}
		oldCount, newCount := 0, 0
		for _, op := range ops[from:to] {
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}
		// An empty side starts at the line before it, as in unified diffs.
		if oldCount == 0 {
			oldLine--
		}
		if newCount == 0 {
			newLine--
		}
		_, _ = fmt.Fprintln(w, lineStyle('@').Render(fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldLine, oldCount, newLine, newCount)))
		for _, op := range ops[from:to] {
			_, _ = fmt.Fprintln(w, lineStyle(op.kind).Render(string(op.kind)+op.line))
		}
		start = to
	}
}
