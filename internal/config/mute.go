package config

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rm4n0s/errors"
)

// TagMuteMarkCorrupt: the marker of the muted output is not JSON of a
// device.
const TagMuteMarkCorrupt = "ConfigMuteMarkCorrupt"

// MuteFile is the marker of the output device mardp muted (SPEC §10.1).
// It is there while the Mac's output is muted for a client; one found at
// start was left by a mardp that did not unmute it.
const MuteFile = "muted.json"

// muteMark is what MuteFile holds.
type muteMark struct {
	// Device is the UID of the Core Audio device.
	Device string `json:"device"`
}

// MarkMuted writes the marker of the output device uid.
func (f *Folder) MarkMuted(ctx context.Context, uid string) error {
	data, err := json.Marshal(muteMark{Device: uid})
	if err != nil {
		return errors.NewErr(TagWriteFailed, err)
	}
	return f.write(ctx, MuteFile, append(data, '\n'))
}

// MutedMark reads the marker: the UID of the device it names, and ok
// unset when there is none.
func (f *Folder) MutedMark(ctx context.Context) (uid string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, errors.NewErr(TagReadFailed, err)
	}
	data, ok, err := f.readFile(MuteFile)
	if err != nil || !ok {
		return "", false, err
	}
	var m muteMark
	if err := json.Unmarshal(data, &m); err != nil {
		return "", false, errors.NewErr(TagMuteMarkCorrupt, err).SetMetadata("path", filepath.Join(f.Path, MuteFile))
	}
	if m.Device == "" {
		return "", false, errors.New(TagMuteMarkCorrupt, "the marker names no device",
			"path", filepath.Join(f.Path, MuteFile))
	}
	return m.Device, true, nil
}

// ClearMuted removes the marker; there being none is not a failure.
func (f *Folder) ClearMuted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.NewErr(TagWriteFailed, err)
	}
	p := filepath.Join(f.Path, MuteFile)
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.NewErr(TagWriteFailed, err).SetMetadata("path", p)
	}
	return nil
}
