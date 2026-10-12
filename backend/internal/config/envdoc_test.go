package config_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// envReaders are the calls whose string arguments are environment names.
var envReaders = map[string]bool{
	"Getenv":             true,
	"LookupEnv":          true,
	"getEnvWithFallback": true,
	"getEnvWithDefault":  true,
	"firstEnv":           true,
	"lookupNonEmpty":     true,
}

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// TestEnvironmentDocCoversEveryVariable fails when the backend reads an
// environment variable that docs/environment.md does not list.
func TestEnvironmentDocCoversEveryVariable(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "environment.md"))
	if err != nil {
		t.Fatal(err)
	}
	names := envNamesInSource(t, filepath.Join("..", ".."))
	if len(names) < 50 {
		t.Fatalf("found only %d environment names; the scan is broken", len(names))
	}
	var missing []string
	for _, name := range names {
		if !strings.Contains(string(doc), "`"+name+"`") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/environment.md is missing: %s", strings.Join(missing, ", "))
	}
}

func envNamesInSource(t *testing.T, root string) []string {
	t.Helper()
	fset := token.NewFileSet()
	found := map[string]bool{}
	pkgConsts := map[string]map[string]string{}
	var files []*ast.File
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "frontend" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		files = append(files, f)
		dirs = append(dirs, filepath.Dir(path))
		consts := pkgConsts[filepath.Dir(path)]
		if consts == nil {
			consts = map[string]string{}
			pkgConsts[filepath.Dir(path)] = consts
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, ident := range vs.Names {
					if i < len(vs.Values) {
						if s, ok := stringLit(vs.Values[i]); ok {
							consts[ident.Name] = s
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	add := func(s string) {
		s = strings.TrimPrefix(s, "VITE_")
		if envName.MatchString(s) {
			found[s] = true
		}
	}
	for i, f := range files {
		consts := pkgConsts[dirs[i]]
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				name := ""
				switch fn := n.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				}
				if !envReaders[name] {
					return true
				}
				args := n.Args
				if name == "getEnvWithDefault" && len(args) > 1 {
					args = args[:1]
				}
				for _, arg := range args {
					if s, ok := stringLit(arg); ok {
						add(s)
					} else if id, ok := arg.(*ast.Ident); ok {
						if s, ok := consts[id.Name]; ok {
							add(s)
						}
					}
				}
			case *ast.CompositeLit:
				// Lists of names checked in a loop, such as the static key set.
				var lits []string
				for _, elt := range n.Elts {
					s, ok := stringLit(elt)
					if !ok || !envName.MatchString(s) {
						return true
					}
					lits = append(lits, s)
				}
				for _, s := range lits {
					add(s)
				}
			}
			return true
		})
	}
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}
