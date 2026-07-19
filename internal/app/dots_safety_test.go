package app

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestDotsLocalTargetRemovalsUseBackupGuards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file      string
		forbidden []string
	}{
		{file: "dots.go", forbidden: []string{"entry.TargetPath", "targetPath", "originalPath"}},
		{file: filepath.Join("..", "dots", "linker.go"), forbidden: []string{"e.TargetPath", "dst"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, tc.file, nil, 0)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}
			ast.Inspect(parsed, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Remove" && sel.Sel.Name != "RemoveAll") {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "os" {
					return true
				}
				arg, err := exprString(fset, call.Args[0])
				if err != nil {
					t.Fatalf("print os.%s argument: %v", sel.Sel.Name, err)
				}
				for _, forbidden := range tc.forbidden {
					if arg == forbidden || strings.Contains(arg, forbidden) {
						t.Errorf("%s:%d uses os.%s(%s); use dots backup guard helpers for local targets",
							tc.file, fset.Position(call.Pos()).Line, sel.Sel.Name, arg)
					}
				}
				return true
			})
		})
	}
}

func exprString(fset *token.FileSet, expr ast.Expr) (string, error) {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "", err
	}
	return buf.String(), nil
}
