package plugins

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestPluginPackagesDoNotImportOtherPlugins(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate import-boundary test source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))
	pluginsRoot := filepath.Join(repoRoot, "plugins")

	err := filepath.WalkDir(pluginsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if importPath == "sunkenbot/plugins" || strings.HasPrefix(importPath, "sunkenbot/plugins/") {
				t.Errorf("%s imports plugin package %q; move shared code under internal/", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan plugin imports: %v", err)
	}
}

// الاتجاه الآخر: internal/ مشترك لكل الخدمات ولا يجوز أن يعتمد على أي منها.
func TestInternalPackagesDoNotImportPlugins(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate import-boundary test source")
	}
	internalRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), ".."))

	checked := 0
	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if importPath == "sunkenbot/plugins" || strings.HasPrefix(importPath, "sunkenbot/plugins/") {
				t.Errorf("%s imports %q; internal/ must not depend on plugins/", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal imports: %v", err)
	}
	if checked < 10 {
		t.Fatalf("scanned only %d files; wrong root?", checked)
	}
}
