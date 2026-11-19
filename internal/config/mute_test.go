package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMuteMark(t *testing.T) {
	ctx := context.Background()
	f, err := Open(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.MutedMark(ctx); ok || err != nil {
		t.Fatalf("a marker at first: %v, %v", ok, err)
	}
	if err := f.ClearMuted(ctx); err != nil {
		t.Fatalf("clearing no marker: %v", err)
	}
	if err := f.MarkMuted(ctx, "BuiltInSpeakerDevice"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(f.Path, MuteFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != FileMode {
		t.Errorf("mode %v", st.Mode())
	}
	uid, ok, err := f.MutedMark(ctx)
	if err != nil || !ok || uid != "BuiltInSpeakerDevice" {
		t.Fatalf("MutedMark = %q, %v, %v", uid, ok, err)
	}
	if err := f.ClearMuted(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.MutedMark(ctx); ok || err != nil {
		t.Fatalf("a marker after clearing: %v, %v", ok, err)
	}
}

func TestMuteMarkFails(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name  string
		setup func(t *testing.T, f *Folder)
		run   func(ctx context.Context, f *Folder) error
		ctx   context.Context
		route string
	}{
		{"mark: canceled", nil, func(ctx context.Context, f *Folder) error { return f.MarkMuted(ctx, "x") },
			canceled, "Folder.MarkMuted->Folder.write.ConfigWriteFailed"},
		{"read: canceled", nil, func(ctx context.Context, f *Folder) error {
			_, _, err := f.MutedMark(ctx)
			return err
		}, canceled, "Folder.MutedMark.ConfigReadFailed"},
		{"read: a folder", func(t *testing.T, f *Folder) {
			if err := os.Mkdir(filepath.Join(f.Path, MuteFile), 0o700); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, f *Folder) error {
			_, _, err := f.MutedMark(ctx)
			return err
		}, nil, "Folder.MutedMark->Folder.readFile.ConfigReadFailed"},
		{"read: not JSON", func(t *testing.T, f *Folder) {
			if err := os.WriteFile(filepath.Join(f.Path, MuteFile), []byte("muted"), FileMode); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, f *Folder) error {
			_, _, err := f.MutedMark(ctx)
			return err
		}, nil, "Folder.MutedMark.ConfigMuteMarkCorrupt"},
		{"read: no device", func(t *testing.T, f *Folder) {
			if err := os.WriteFile(filepath.Join(f.Path, MuteFile), []byte(`{"device":""}`), FileMode); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, f *Folder) error {
			_, _, err := f.MutedMark(ctx)
			return err
		}, nil, "Folder.MutedMark.ConfigMuteMarkCorrupt"},
		{"clear: canceled", nil, func(ctx context.Context, f *Folder) error { return f.ClearMuted(ctx) },
			canceled, "Folder.ClearMuted.ConfigWriteFailed"},
		{"clear: a folder with a file", func(t *testing.T, f *Folder) {
			d := filepath.Join(f.Path, MuteFile)
			if err := os.Mkdir(d, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, "x"), nil, FileMode); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, f *Folder) error { return f.ClearMuted(ctx) },
			nil, "Folder.ClearMuted.ConfigWriteFailed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Open(context.Background(), t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.setup != nil {
				tt.setup(t, f)
			}
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			assertRoute(t, tt.run(ctx, f), tt.route)
		})
	}
}
