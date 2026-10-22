// Copyright 2026 The pgtenant Authors
// SPDX-License-Identifier: MIT

// Package pgtenant_test keeps each code excerpt the README shows a verbatim
// part of its example, keeps the quick start example the same as the
// pkg.go.dev Example, and checks the examples runner.
package pgtenant_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"testing"
)

// goBlockAfter returns the first go block after the heading line.
func goBlockAfter(t *testing.T, heading string) string {
	t.Helper()
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(readme), "\n"+heading+"\n")
	if !ok {
		t.Fatalf("README has no %q heading", heading)
	}
	_, rest, ok = strings.Cut(rest, "\n```go\n")
	if !ok {
		t.Fatalf("README has no go block after %q", heading)
	}
	block, _, ok := strings.Cut(rest, "\n```\n")
	if !ok {
		t.Fatal("unterminated go block")
	}
	return block + "\n"
}

// funcBody prints the body of the named function in file, without comments.
func funcBody(t *testing.T, file, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
			var b bytes.Buffer
			if err := printer.Fprint(&b, fset, fn.Body); err != nil {
				t.Fatal(err)
			}
			return b.String()
		}
	}
	t.Fatalf("%s has no func %s", file, name)
	return ""
}

// TestReadmeExcerptsMatchExamples: each blank-line-separated part of the go
// block under a heading appears verbatim in its example file.
func TestReadmeExcerptsMatchExamples(t *testing.T) {
	tests := []struct {
		heading  string
		mainFile string
	}{
		{"## Quick start", "examples/read-own-rows/main.go"},
		{"### Scope DAO methods", "examples/dao-scope/main.go"},
	}
	for _, tt := range tests {
		t.Run(tt.heading, func(t *testing.T) {
			want, err := os.ReadFile(tt.mainFile)
			if err != nil {
				t.Fatal(err)
			}
			for _, part := range strings.Split(goBlockAfter(t, tt.heading), "\n\n") {
				if !strings.Contains(string(want), part) {
					t.Errorf("README excerpt under %q is not in %s:\n%s", tt.heading, tt.mainFile, part)
				}
			}
		})
	}
}

func TestQuickStartMatchesExample(t *testing.T) {
	const mainFile = "examples/read-own-rows/main.go"
	if a, b := funcBody(t, mainFile, "main"), funcBody(t, "tenantdb/example_test.go", "ExampleTenantTx"); a != b {
		t.Fatalf("ExampleTenantTx body differs from main:\n%s\n---\n%s", b, a)
	}
}
