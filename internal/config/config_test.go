package config

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rm4n0s/errors"
)

// assertRoute fails t unless err is an *errors.Error with the route.
func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s, want %s", e.Route(), route)
	}
}

// recorder is a slog.Handler that keeps the records.
type recorder struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *recorder) WithGroup(string) slog.Handler            { return r }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec.Clone())
	return nil
}

func (r *recorder) count(msg string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.recs {
		if rec.Message == msg {
			n++
		}
	}
	return n
}

// all is every record's message and attributes, for a check that nothing
// secret is logged.
func (r *recorder) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, rec := range r.recs {
		b.WriteString(rec.Message)
		rec.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.String())
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

func mode(t *testing.T, p string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestDir(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == HomeEnv {
				return v
			}
			return ""
		}
	}
	d, err := Dir(env("/tmp/x/../mardp-home"))
	if err != nil || d != "/tmp/mardp-home" {
		t.Errorf("Dir with MARDP_HOME = %q, %v", d, err)
	}
	d, err = Dir(env("rel"))
	if err != nil || !filepath.IsAbs(d) || filepath.Base(d) != "rel" {
		t.Errorf("Dir with a relative MARDP_HOME = %q, %v", d, err)
	}
	t.Setenv("HOME", "/Users/someone")
	t.Setenv("XDG_CONFIG_HOME", "")
	d, err = Dir(env(""))
	if err != nil || filepath.Base(d) != "mardp" || !strings.HasPrefix(d, "/Users/someone/") {
		t.Errorf("Dir without MARDP_HOME = %q, %v", d, err)
	}
	t.Setenv("HOME", "")
	_, err = Dir(env(""))
	assertRoute(t, err, "Dir."+TagHomeMissing)
}

func TestOpenModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]fs.FileMode{FileName: 0o644, keyFile: 0o666, certFile: 0o600} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, m); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, name), m); err != nil {
			t.Fatal(err)
		}
	}
	rec := &recorder{}
	if _, err := Open(context.Background(), dir, slog.New(rec)); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, dir); m != FolderMode {
		t.Errorf("folder mode %v", m)
	}
	for _, name := range []string{FileName, keyFile, certFile} {
		if m := mode(t, filepath.Join(dir, name)); m != FileMode {
			t.Errorf("%s mode %v", name, m)
		}
	}
	if n := rec.count("the mode of a file of mardp's folder was corrected"); n != 3 {
		t.Errorf("%d corrections logged, want 3", n)
	}

	// A new folder is made with its mode, missing parents too.
	fresh := filepath.Join(t.TempDir(), "a", "b")
	if _, err := Open(context.Background(), fresh, nil); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, fresh); m != FolderMode {
		t.Errorf("new folder mode %v", m)
	}
}

func TestOpenFails(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "linked")
	if err := os.Mkdir(link, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(link, keyFile)); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name  string
		ctx   context.Context
		path  string
		route string
	}{
		{"canceled", canceled, base, "Open." + TagFolderFailed},
		{"under a file", context.Background(), filepath.Join(file, "home"), "Open." + TagFolderFailed},
		{"symbolic link", context.Background(), link, "Open->Folder.fixMode." + TagFolderFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Open(tt.ctx, tt.path, nil)
			assertRoute(t, err, tt.route)
		})
	}
}

// folderWith returns a folder whose config.json holds data.
func folderWith(t *testing.T, data string) *Folder {
	t.Helper()
	f, err := Open(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if data != "" {
		if err := os.WriteFile(filepath.Join(f.Path, FileName), []byte(data), FileMode); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestLoad(t *testing.T) {
	custom := Defaults()
	custom.User, custom.Port, custom.Address, custom.MaxFPS, custom.Sound, custom.SwapCtrlCommand = "manos", 3390, "192.168.1.5", 60, SoundBoth, true
	customJSON, err := json.Marshal(custom)
	if err != nil {
		t.Fatal(err)
	}
	partial := Defaults()
	partial.User = "a"
	tests := []struct {
		name  string
		data  string
		want  Settings
		route string
	}{
		{"first start", "", Defaults(), ""},
		{"all settings", string(customJSON), custom, ""},
		{"missing settings are the defaults", `{"version":1,"user":"a"}`, partial, ""},
		{"unknown settings are ignored", `{"version":1,"user":"a","later":true}`, partial, ""},
		{"newer version", `{"version":3,"user":"a"}`, Settings{}, "Folder.Load." + TagTooNew},
		{"no version", `{"user":"a"}`, Settings{}, "Folder.Load." + TagVersionMissing},
		{"version not a number", `{"version":"one"}`, Settings{}, "Folder.Load." + TagCorrupt},
		{"version 0", `{"version":0}`, Settings{}, "Folder.Load." + TagCorrupt},
		{"not JSON", `{"version":1`, Settings{}, "Folder.Load." + TagCorrupt},
		{"wrong type", `{"version":1,"port":"x"}`, Settings{}, "Folder.Load." + TagCorrupt},
		{"port out of range", `{"version":1,"port":70000}`, Settings{}, "Settings.Validate." + TagInvalid},
		{"address", `{"version":1,"address":"nowhere"}`, Settings{}, "Settings.Validate." + TagInvalid},
		{"frame rate", `{"version":1,"maxFps":45}`, Settings{}, "Settings.Validate." + TagInvalid},
		{"version 1's frame rate of 30 becomes 60", `{"version":1,"user":"a","maxFps":30}`, partial, ""},
		{"version 2 keeps 30", `{"version":2,"user":"a","maxFps":30}`, func() Settings { s := partial; s.MaxFPS = 30; return s }(), ""},
		{"height cap", `{"version":1,"maxHeight":-1}`, Settings{}, "Settings.Validate." + TagInvalid},
		{"bitrate", `{"version":1,"maxBitrate":5}`, Settings{}, "Settings.Validate." + TagInvalid},
		{"sound", `{"version":1,"sound":"loud"}`, Settings{}, "Settings.Validate." + TagInvalid},
		{"damaged hash", `{"version":1,"password":{"salt":"AAAAAAAAAAA=","iterations":100000000,"hash":"AAAA"}}`, Settings{}, "PasswordHash.check." + TagPasswordHashInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := folderWith(t, tt.data).Load(context.Background())
			if tt.route != "" {
				assertRoute(t, err, tt.route)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gj, wj := mustJSON(t, got), mustJSON(t, tt.want); gj != wj {
				t.Errorf("Load =\n%s\nwant\n%s", gj, wj)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTooNewMessage(t *testing.T) {
	_, err := folderWith(t, `{"version":7}`).Load(context.Background())
	e, ok := errors.FromError(err)
	if !ok || !strings.Contains(e.Message, "newer mardp (version 7; this mardp reads up to 2)") {
		t.Errorf("message %v", err)
	}
}

func TestUpgrade(t *testing.T) {
	rec := &recorder{}
	f := folderWith(t, `{"version":1,"fps":60,"user":"a"}`)
	f.log = slog.New(rec)
	// Version 2 renamed fps to maxFps.
	f.upgrades = []func(map[string]json.RawMessage) error{func(m map[string]json.RawMessage) error {
		m["maxFps"] = m["fps"]
		delete(m, "fps")
		return nil
	}}
	s, err := f.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.MaxFPS != 60 || s.User != "a" || s.Version != 2 {
		t.Errorf("upgraded %+v", s)
	}
	if rec.count("config.json was upgraded") != 1 {
		t.Error("the upgrade was not logged")
	}
	if err := f.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(f.Path, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version": 2`) {
		t.Errorf("saved %s", data)
	}

	f.upgrades[0] = func(map[string]json.RawMessage) error { return errors.New("TestUpgradeBroken", "no") }
	if err := os.WriteFile(filepath.Join(f.Path, FileName), []byte(`{"version":1}`), FileMode); err != nil {
		t.Fatal(err)
	}
	_, err = f.Load(context.Background())
	assertRoute(t, err, "Folder.Load."+TagUpgradeFailed)
}

func TestSave(t *testing.T) {
	f := folderWith(t, "")
	s := Defaults()
	s.User = "manos"
	var err error
	if s.Password, err = hashPassword("correct horse", 1000); err != nil {
		t.Fatal(err)
	}
	s.Version = 0
	if err := f.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, filepath.Join(f.Path, FileName)); m != FileMode {
		t.Errorf("config.json mode %v", m)
	}
	got, err := f.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.Version != Version || !got.CheckLogin("manos", "correct horse") {
		t.Errorf("loaded %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(f.Path, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "correct horse") {
		t.Error("the password is in config.json")
	}
	entries, err := os.ReadDir(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("folder holds %d files, want config.json alone", len(entries))
	}
}

func TestSaveFails(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	bad := Defaults()
	bad.Port = 0
	gone := folderWith(t, "")
	if err := os.Remove(gone.Path); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		ctx   context.Context
		f     *Folder
		s     Settings
		route string
	}{
		{"invalid", context.Background(), folderWith(t, ""), bad, "Settings.Validate." + TagInvalid},
		{"canceled", canceled, folderWith(t, ""), Defaults(), "Folder.write." + TagWriteFailed},
		{"no folder", context.Background(), gone, Defaults(), "Folder.write." + TagWriteFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, tt.f.Save(tt.ctx, tt.s), tt.route)
		})
	}
}

func TestLoadFails(t *testing.T) {
	f := folderWith(t, "")
	if err := os.Mkdir(filepath.Join(f.Path, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := f.Load(context.Background())
	assertRoute(t, err, "Folder.Load."+TagReadFailed)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = folderWith(t, "").Load(canceled)
	assertRoute(t, err, "Folder.Load."+TagReadFailed)
}
