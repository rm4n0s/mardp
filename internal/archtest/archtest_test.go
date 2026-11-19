package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
)

const testModule = "github.com/rm4n0s/mardp"

// TestRepository is the check itself: the rules hold for this repository.
func TestRepository(t *testing.T) {
	vs, err := Check(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestForbidden(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		imp  string
		test bool
		want string // a part of the reason, "" when allowed
	}{
		{"rdp stdlib", "rdp", "net", false, ""},
		{"rdp errors", "rdp", errorsModule, false, ""},
		{"rdp its internal", "rdp", testModule + "/rdp/internal/wire", false, ""},
		{"rdp internal sibling", "rdp/internal/x224", testModule + "/rdp/internal/wire", false, ""},
		{"rdp cgo", "rdp", "C", false, `import "C"`},
		{"rdp glutter", "rdp/internal/gfx", glutterModule + "/widgets", false, "Glutter"},
		{"rdp host", "rdp", testModule + "/internal/host", false, "outside rdp/"},
		{"rdp other module", "rdp", "golang.org/x/crypto/md4", false, "dependency"},
		{"rdp test uses rdptest", "rdp", testModule + "/rdp/rdptest", true, ""},
		{"rdp code uses rdptest", "rdp", testModule + "/rdp/rdptest", false, "only tests"},
		{"rdptest uses internal", "rdp/rdptest", testModule + "/rdp/internal/bitmap", false, ""},
		{"host rdp", "internal/host", testModule + "/rdp", false, ""},
		{"host rdptest in test", "internal/host", testModule + "/rdp/rdptest", true, ""},
		{"host rdptest in code", "internal/host", testModule + "/rdp/rdptest", false, "only rdp"},
		{"host mac", "internal/host", testModule + "/internal/mac/capture", false, "only rdp"},
		{"host config", "internal/host", testModule + "/internal/config", false, "only rdp"},
		{"host cgo", "internal/host", "C", false, `import "C"`},
		{"mac cgo", "internal/mac/capture", "C", false, ""},
		{"mac host", "internal/mac/vtenc", testModule + "/internal/host", false, ""},
		{"mac sibling", "internal/mac/capture", testModule + "/internal/mac/system", false, ""},
		{"mac rdp", "internal/mac/capture", testModule + "/rdp", false, "only internal/host"},
		{"mac glutter", "internal/mac/system", glutterModule, false, "Glutter"},
		{"config stdlib", "internal/config", "crypto/pbkdf2", false, ""},
		{"config host", "internal/config", testModule + "/internal/host", false, "nothing of mardp"},
		{"ui glutter", "internal/ui", glutterModule + "/widgets", false, ""},
		{"ui host", "internal/ui", testModule + "/internal/host", false, ""},
		{"ui config", "internal/ui", testModule + "/internal/config", false, ""},
		{"ui rdp", "internal/ui", testModule + "/rdp", false, "only internal/host"},
		{"ui cgo", "internal/ui", "C", false, `import "C"`},
		{"cmd anything own", "cmd/mardp", testModule + "/internal/mac/capture", false, ""},
		{"cmd glutter", "cmd/mardp", glutterModule, false, "Glutter"},
		{"spikes are gone", "spikes/probe", "fmt", false, "not in the table"},
		{"spikes cgo", "spikes/probe", "C", false, `import "C"`},
		{"archtest stdlib", "internal/archtest", "go/parser", false, ""},
		{"archtest own", "internal/archtest", testModule + "/rdp", false, "nothing of mardp"},
		{"unknown package", "internal/foo", "fmt", false, "not in the table"},
		{"module root", ".", "fmt", false, "module root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forbidden(testModule, tt.dir, tt.imp, tt.test)
			if tt.want == "" && got != "" {
				t.Errorf("forbidden = %q, want allowed", got)
			}
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("forbidden = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// writeTree creates the files under a new temporary root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCheckFindsPlantedViolation(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":                    "module " + testModule + "\n\ngo 1.27\n",
		"rdp/server.go":             "package rdp\n\nimport (\n\t\"net\"\n\n\t\"" + testModule + "/internal/host\"\n)\n\nvar _ net.Conn\nvar _ = host.X\n",
		"rdp/testdata/bad.go":       "package bad\n\nimport \"C\"\n",
		"internal/mac/capture/c.go": "package capture\n\n// #include <stdio.h>\nimport \"C\"\n",
		"internal/host/host.go":     "package host\n\nimport \"C\"\n",
		"internal/ui/ui.go":         "package ui\n\nimport _ \"" + glutterModule + "/widgets\"\n",
		"internal/ui/README.txt":    "not Go\n",
	})
	vs, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	want := []Violation{
		{File: "internal/host/host.go", Import: "C"},
		{File: "rdp/server.go", Import: testModule + "/internal/host"},
	}
	if len(vs) != len(want) {
		t.Fatalf("violations = %v, want %v", vs, want)
	}
	for i := range want {
		if vs[i].File != want[i].File || vs[i].Import != want[i].Import || vs[i].Reason == "" {
			t.Errorf("violation %d = %v, want file %s import %s", i, vs[i], want[i].File, want[i].Import)
		}
	}
}

func TestCheckEmptyTree(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": "module " + testModule + "\n"})
	vs, err := Check(root)
	if err != nil || len(vs) != 0 {
		t.Fatalf("Check = %v, %v; want no violations", vs, err)
	}
}

func TestCheckFailures(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		setup func(t *testing.T, root string)
		route string
	}{
		{
			name:  "no go.mod",
			files: map[string]string{"x.txt": ""},
			route: "Check->readModulePath." + TagModuleFileReadFailed,
		},
		{
			name:  "go.mod without module line",
			files: map[string]string{"go.mod": "go 1.27\n"},
			route: "Check->readModulePath." + TagModulePathMissing,
		},
		{
			name:  "Go file that does not parse",
			files: map[string]string{"go.mod": "module m\n", "rdp/x.go": "package rdp\n\nimport (\n"},
			route: "Check.func1." + TagSourceParseFailed,
		},
		{
			name:  "folder that cannot be read",
			files: map[string]string{"go.mod": "module m\n", "rdp/x.go": "package rdp\n"},
			setup: func(t *testing.T, root string) {
				dir := filepath.Join(root, "rdp")
				if err := os.Chmod(dir, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			},
			route: "Check.func1." + TagTreeWalkFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			if tt.setup != nil {
				tt.setup(t, root)
			}
			_, err := Check(root)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if !strings.HasPrefix(appErr.Route(), "Check") || !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
		})
	}
}
