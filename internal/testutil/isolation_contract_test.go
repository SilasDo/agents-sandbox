package testutil

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// fullSetInstaller is the wrapper that installs every seam family at once.
const fullSetInstaller = "InitFailFastMocks"

// seamFamily ties a fail-fast family to the package that owns it and the
// exported function that installs it.
type seamFamily struct {
	name        string
	dir         string
	packageName string
	installer   string
}

var seamFamilies = []seamFamily{
	{
		name:        "configpaths",
		dir:         "internal/configpaths",
		packageName: "configpaths",
		installer:   "InstallFailFastConfigPaths",
	},
	{name: "docker", dir: "internal/sandbox/docker", packageName: "docker", installer: "InstallFailFastGet"},
	{name: "doctor", dir: "internal/sandbox/doctor", packageName: "doctor", installer: "InstallFailFast"},
	{name: "msb", dir: "internal/sandbox/msb", packageName: "msb", installer: "InstallFailFastGet"},
}

// TestEveryTestPackageInstallsItsAcyclicFailFastMocks enforces the
// test-isolation contract: a package with tests must install, from main_test.go's
// TestMain, the fail-fast default for every seam family its tests can reach
// without an import cycle. The expected set is derived from the module's import
// graph, so this test needs no hand-maintained per-package exception list: a
// family is skipped only when importing it would close a cycle.
func TestEveryTestPackageInstallsItsAcyclicFailFastMocks(t *testing.T) {
	root := moduleRoot(t)
	modulePath := moduleImportPath(t, root)
	packages := modulePackages(t, root)
	if len(packages) == 0 {
		t.Fatal("no Go packages found; is the module root correct?")
	}
	transitive := transitiveModuleImports(t, root, modulePath, packages)

	checked := 0
	for _, pkg := range packages {
		if !pkg.hasTests {
			continue
		}
		checked++
		want := acyclicSeamFamilies(pkg.dir, transitive)
		got, err := installedSeamFamilies(root, pkg.dir, modulePath)
		if err != nil {
			t.Errorf("%s: %v", pkg.dir, err)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: fail-fast mocks installed = %v, want %v", pkg.dir, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("no test packages found; is the module root correct?")
	}
}

type modulePackage struct {
	dir        string
	hasNonTest bool
	hasTests   bool
}

// moduleRoot walks up from the working directory to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// moduleImportPath reads the module path declared in go.mod.
func moduleImportPath(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if trimmed, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(trimmed)
		}
	}
	t.Fatal("go.mod has no module directive")
	return ""
}

// modulePackages lists every directory under root holding at least one .go file,
// recording whether it holds non-test and test sources.
func modulePackages(t *testing.T, root string) []modulePackage {
	t.Helper()
	var packages []modulePackage
	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if p == root {
			return nil
		}
		if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		var pkg modulePackage
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		pkg.dir = filepath.ToSlash(rel)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				pkg.hasTests = true
			} else {
				pkg.hasNonTest = true
			}
		}
		if pkg.hasNonTest || pkg.hasTests {
			packages = append(packages, pkg)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].dir < packages[j].dir })
	return packages
}

// transitiveModuleImports resolves, for every package, the set of module-local
// packages it imports directly or indirectly (non-test imports only).
func transitiveModuleImports(
	t *testing.T,
	root, modulePath string,
	packages []modulePackage,
) map[string]map[string]bool {
	t.Helper()
	known := map[string]bool{}
	for _, pkg := range packages {
		if pkg.hasNonTest {
			known[pkg.dir] = true
		}
	}
	direct := map[string]map[string]bool{}
	for dir := range known {
		direct[dir] = directModuleImports(t, filepath.Join(root, dir), modulePath, known)
	}
	transitive := map[string]map[string]bool{}
	var resolve func(dir string) map[string]bool
	resolve = func(dir string) map[string]bool {
		if cached, ok := transitive[dir]; ok {
			return cached
		}
		transitive[dir] = map[string]bool{} // cycle guard
		out := map[string]bool{}
		for dep := range direct[dir] {
			out[dep] = true
			for nested := range resolve(dep) {
				out[nested] = true
			}
		}
		transitive[dir] = out
		return out
	}
	for dir := range known {
		resolve(dir)
	}
	return transitive
}

// directModuleImports returns the module-local packages imported by the non-test
// files in absDir, honouring build constraints.
func directModuleImports(t *testing.T, absDir, modulePath string, known map[string]bool) map[string]bool {
	t.Helper()
	pkg, err := build.Default.ImportDir(absDir, build.ImportComment)
	if err != nil {
		if _, ok := errors.AsType[*build.NoGoError](err); !ok {
			t.Fatalf("import dir %s: %v", absDir, err)
		}
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, imported := range pkg.Imports {
		rel, ok := strings.CutPrefix(imported, modulePath+"/")
		if ok && known[rel] {
			out[rel] = true
		}
	}
	return out
}

// acyclicSeamFamilies returns the families whose fail-fast a package's tests can
// install: its own family, plus every family that does not itself import the
// package (importing such a family from the package's internal test would cycle).
func acyclicSeamFamilies(pkgDir string, transitive map[string]map[string]bool) []string {
	var families []string
	for _, family := range seamFamilies {
		if family.dir == pkgDir || !transitive[family.dir][pkgDir] {
			families = append(families, family.name)
		}
	}
	sort.Strings(families)
	return families
}

// installedSeamFamilies parses a package's main_test.go and reports the seam
// families its TestMain installs.
func installedSeamFamilies(root, pkgDir, modulePath string) ([]string, error) {
	mainTestPath := filepath.Join(root, pkgDir, "main_test.go")
	file, err := parser.ParseFile(token.NewFileSet(), mainTestPath, nil, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("no main_test.go (add one with a TestMain that installs the fail-fast mocks)")
		}
		return nil, fmt.Errorf("parse main_test.go: %w", err)
	}
	testMain := findTestMain(file)
	if testMain == nil {
		return nil, errors.New("main_test.go has no TestMain")
	}

	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		local := path.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = importPath
	}

	installed := map[string]bool{}
	ast.Inspect(testMain.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			base, ok := fun.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath := imports[base.Name]
			if importPath == modulePath+"/internal/testutil/mocks" && fun.Sel.Name == fullSetInstaller {
				for _, family := range seamFamilies {
					installed[family.name] = true
				}
			}
			for _, family := range seamFamilies {
				if importPath == modulePath+"/"+family.dir && fun.Sel.Name == family.installer {
					installed[family.name] = true
				}
			}
		case *ast.Ident:
			for _, family := range seamFamilies {
				if file.Name.Name == family.packageName && fun.Name == family.installer {
					installed[family.name] = true
				}
			}
		}
		return true
	})

	families := make([]string, 0, len(installed))
	for name := range installed {
		families = append(families, name)
	}
	sort.Strings(families)
	return families, nil
}

func findTestMain(file *ast.File) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "TestMain" {
			return fn
		}
	}
	return nil
}
