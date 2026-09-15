// Command codecheck enforces the repository's physical-line limits.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	failed := false
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "bin" || entry.Name() == ".references" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".py", ".sh", ".js", ".ts", ".tsx", ".jsx", ".rs", ".c", ".cpp", ".h":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		count := strings.Count(strings.TrimSuffix(string(data), "\n"), "\n") + 1
		if count > 1000 {
			fmt.Printf("%s: code file has %d lines (maximum 1000); split it\n", path, count)
			failed = true
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, data, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			var name string
			switch n := node.(type) {
			case *ast.FuncDecl:
				name = n.Name.Name
			case *ast.FuncLit:
				name = "function literal"
			default:
				return true
			}
			start, end := fset.Position(node.Pos()).Line, fset.Position(node.End()).Line
			if end-start+1 > 100 {
				fmt.Printf("%s:%d: %s has %d lines (maximum 100); split it\n", path, start, name, end-start+1)
				failed = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if failed {
		os.Exit(1)
	}
}
