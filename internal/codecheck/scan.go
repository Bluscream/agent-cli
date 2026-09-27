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

// Limits are the repository's physical-line limits. FileWarn is a notice rather
// than a failure: it marks the seam while splitting is still cheap.
const (
	FileLimit = 1000
	FileWarn  = 600
	FuncLimit = 100
)

var (
	skipDirs  = map[string]bool{".git": true, "bin": true, ".references": true}
	codeExts  = map[string]bool{".go": true, ".py": true, ".sh": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".rs": true, ".c": true, ".cpp": true, ".h": true}
	anonymous = "function literal"
)

// Violation is one limit breach.
type Violation struct {
	Path   string
	Line   int
	Symbol string // function name, "function literal", or "" for a whole file
	Lines  int
	Limit  int
}

// Key identifies a violation independently of its line number, so a baseline
// entry survives unrelated edits that shift code up or down the file.
func (v Violation) Key() string {
	if v.Symbol == "" {
		return v.Path
	}
	return v.Path + ":" + v.Symbol
}

func (v Violation) String() string {
	if v.Symbol == "" {
		return fmt.Sprintf("%s:%d: file has %d lines (maximum %d); split it", v.Path, v.Line, v.Lines, v.Limit)
	}
	return fmt.Sprintf("%s:%d: %s has %d lines (maximum %d); split it", v.Path, v.Line, v.Symbol, v.Lines, v.Limit)
}

// Scan walks root and reports every limit breach, plus files approaching the
// file limit as notices.
func Scan(root string) (violations []Violation, notices []string, err error) {
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !codeExts[filepath.Ext(path)] {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		count := strings.Count(strings.TrimSuffix(string(data), "\n"), "\n") + 1
		switch {
		case count > FileLimit:
			violations = append(violations, Violation{Path: rel, Line: 1, Lines: count, Limit: FileLimit})
		case count > FileWarn:
			notices = append(notices, fmt.Sprintf(
				"note: %s is %d lines, approaching the %d limit", rel, count, FileLimit))
		}

		if filepath.Ext(path) != ".go" {
			return nil
		}
		found, parseErr := scanFunctions(rel, data)
		if parseErr != nil {
			return parseErr
		}
		violations = append(violations, found...)
		return nil
	})
	return violations, notices, err
}

// scanFunctions reports functions and function literals over the line limit.
func scanFunctions(rel string, data []byte) ([]Violation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, data, 0)
	if err != nil {
		return nil, err
	}

	var found []Violation
	ast.Inspect(file, func(node ast.Node) bool {
		var name string
		switch n := node.(type) {
		case *ast.FuncDecl:
			name = n.Name.Name
		case *ast.FuncLit:
			name = anonymous
		default:
			return true
		}

		start, end := fset.Position(node.Pos()).Line, fset.Position(node.End()).Line
		if lines := end - start + 1; lines > FuncLimit {
			found = append(found, Violation{Path: rel, Line: start, Symbol: name, Lines: lines, Limit: FuncLimit})
		}
		return true
	})
	return found, nil
}
