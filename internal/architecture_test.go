package internal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoreDoesNotDependOnAdaptersOrTransport(t *testing.T) {
	for _, root := range []string{"domain", "application"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, decl := range file.Decls {
				g, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range g.Specs {
					imp, ok := spec.(*ast.ImportSpec)
					if !ok {
						continue
					}
					v, _ := strconv.Unquote(imp.Path.Value)
					if v == "database/sql" || v == "net/http" || strings.Contains(v, "/adapter/") {
						t.Errorf("%s imports %s", path, v)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
