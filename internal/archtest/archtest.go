// Package archtest checks the import rules of the package table in
// docs/SPEC.md §4.1: which package may import what, that `import "C"`
// appears only under internal/mac, and that nothing of the product imports
// the throwaway code under spikes/.
//
// The rules are written as data in this package. A package directory that
// the table does not know is itself a violation, so a new package cannot be
// added without deciding what it may import.
package archtest

import (
	"bufio"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
)

// Tags of the failures of this package.
const (
	TagModuleFileReadFailed = "ModuleFileReadFailed"
	TagModulePathMissing    = "ModulePathMissing"
	TagTreeWalkFailed       = "TreeWalkFailed"
	TagSourceParseFailed    = "SourceParseFailed"
)

const errorsModule = "github.com/rm4n0s/errors"

const glutterModule = "github.com/rm4n0s/glutter"

// Violation is one import, in one file, that the rules forbid.
type Violation struct {
	File   string // path relative to the module root, with forward slashes
	Import string // the import path, "C" for cgo
	Reason string
}

// String formats the violation for a test failure.
func (v Violation) String() string {
	return v.File + ": imports " + strconv.Quote(v.Import) + ": " + v.Reason
}

// Check parses every Go file under root, the folder that holds go.mod, and
// returns the imports that break the rules, sorted by file and import. Build
// constraints are ignored: a file for another system is checked too.
func Check(root string) ([]Violation, error) {
	module, err := readModulePath(root)
	if err != nil {
		return nil, err
	}
	var out []Violation
	fset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.NewErr(TagTreeWalkFailed, err).SetMetadata("path", p)
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return errors.NewErr(TagTreeWalkFailed, err).SetMetadata("path", p)
		}
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return errors.NewErr(TagSourceParseFailed, err).SetMetadata("file", rel)
		}
		dir := path.Dir(rel)
		test := strings.HasSuffix(name, "_test.go")
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return errors.NewErr(TagSourceParseFailed, err).SetMetadata("file", rel)
			}
			if reason := forbidden(module, dir, imp, test); reason != "" {
				out = append(out, Violation{File: rel, Import: imp, Reason: reason})
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Import < out[j].Import
	})
	return out, nil
}

// readModulePath returns the module path written in root/go.mod.
func readModulePath(root string) (string, error) {
	file := filepath.Join(root, "go.mod")
	f, err := os.Open(file)
	if err != nil {
		return "", errors.NewErr(TagModuleFileReadFailed, err).SetMetadata("file", file)
	}
	defer f.Close() //nolint:errcheck // read only; a close error changes nothing
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			if p := strings.Trim(strings.TrimSpace(rest), `"`); p != "" {
				return p, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", errors.NewErr(TagModuleFileReadFailed, err).SetMetadata("file", file)
	}
	return "", errors.New(TagModulePathMissing, "go.mod has no module line", "file", file)
}

// kind is what an import path refers to.
type kind int

const (
	kindCgo kind = iota
	kindStd
	kindErrors
	kindGlutter
	kindOwn   // a package of this module
	kindOther // any other module
)

func classify(module, imp string) kind {
	switch {
	case imp == "C":
		return kindCgo
	case imp == errorsModule || strings.HasPrefix(imp, errorsModule+"/"):
		return kindErrors
	case imp == glutterModule || strings.HasPrefix(imp, glutterModule+"/"):
		return kindGlutter
	case imp == module || strings.HasPrefix(imp, module+"/"):
		return kindOwn
	case !strings.Contains(strings.SplitN(imp, "/", 2)[0], "."):
		return kindStd
	default:
		return kindOther
	}
}

// under reports whether dir is pkg or a folder below it.
func under(dir, pkg string) bool {
	return dir == pkg || strings.HasPrefix(dir, pkg+"/")
}

// forbidden returns why the file in dir may not import imp, or "" when it
// may. test is true for a _test.go file.
func forbidden(module, dir, imp string, test bool) string {
	k := classify(module, imp)
	own := ""
	if k == kindOwn {
		own = strings.TrimPrefix(strings.TrimPrefix(imp, module), "/")
	}

	// Rules that hold everywhere.
	if k == kindOwn && under(own, "spikes") && !under(dir, "spikes") {
		return "the product never imports spikes/ (IMPLEMENTATION.md rule 12)"
	}
	if k == kindCgo && !under(dir, "internal/mac") && !under(dir, "spikes") {
		return `import "C" only under internal/mac (SPEC D14, §4.1)`
	}
	if k == kindGlutter && !under(dir, "internal/ui") && !under(dir, "spikes") {
		return "only internal/ui imports Glutter (SPEC §4.1)"
	}
	if k == kindOther && !under(dir, "spikes") {
		return "a dependency CLAUDE.md does not allow"
	}

	switch {
	case dir == ".":
		return "no Go code at the module root"
	case under(dir, "spikes"), under(dir, "cmd/mardp"):
		// spikes/ is throwaway (rule 12); cmd/mardp wires everything.
		return ""
	case under(dir, "rdp"):
		if k == kindOwn && !under(own, "rdp") {
			return "rdp/ imports nothing of mardp outside rdp/ (SPEC §4.1)"
		}
		if k == kindOwn && under(own, "rdp/rdptest") && !test && !under(dir, "rdp/rdptest") {
			return "only tests import rdp/rdptest"
		}
		return ""
	case dir == "internal/host":
		allowed := own == "rdp" || (test && own == "rdp/rdptest")
		if k == kindOwn && !allowed {
			return "internal/host imports only rdp of mardp (SPEC §4.1)"
		}
		return ""
	case under(dir, "internal/mac"):
		if k == kindOwn && own != "internal/host" && !under(own, "internal/mac") {
			return "internal/mac imports only internal/host and internal/mac of mardp (SPEC §4.1)"
		}
		return ""
	case dir == "internal/config":
		if k == kindOwn {
			return "internal/config imports nothing of mardp (SPEC §4.1)"
		}
		return ""
	case under(dir, "internal/ui"):
		if k == kindOwn && own != "internal/host" && own != "internal/config" && !under(own, "internal/ui") {
			return "internal/ui imports only internal/host and internal/config of mardp (SPEC §4.1)"
		}
		return ""
	case dir == "internal/archtest":
		if k == kindOwn {
			return "internal/archtest imports nothing of mardp"
		}
		return ""
	default:
		return "package " + dir + " is not in the table of SPEC §4.1"
	}
}
