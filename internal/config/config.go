// Package config keeps mardp's settings, its password hash and its TLS
// certificate in mardp's folder (SPEC §8, §13).
package config

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rm4n0s/errors"
)

// Tags of the errors of the settings.
const (
	// TagHomeMissing: neither MARDP_HOME nor the user's configuration
	// folder is known.
	TagHomeMissing = "ConfigHomeMissing"
	// TagFolderFailed: mardp's folder could not be made or its mode set.
	TagFolderFailed = "ConfigFolderFailed"
	// TagReadFailed: a file of the folder could not be read.
	TagReadFailed = "ConfigReadFailed"
	// TagWriteFailed: a file of the folder could not be written.
	TagWriteFailed = "ConfigWriteFailed"
	// TagCorrupt: config.json is not JSON of the settings.
	TagCorrupt = "ConfigCorrupt"
	// TagTooNew: config.json is from a newer mardp.
	TagTooNew = "ConfigTooNew"
	// TagVersionMissing: config.json has no version.
	TagVersionMissing = "ConfigVersionMissing"
	// TagUpgradeFailed: an older config.json could not be upgraded.
	TagUpgradeFailed = "ConfigUpgradeFailed"
	// TagInvalid: a setting is out of its range.
	TagInvalid = "ConfigInvalid"
)

// The folder and its files (SPEC §13).
const (
	// HomeEnv names the folder that replaces the default one.
	HomeEnv = "MARDP_HOME"
	// FileName is the settings file in the folder.
	FileName = "config.json"
	// FolderMode and FileMode are the modes of the folder and of its
	// files: only the user may read them.
	FolderMode fs.FileMode = 0o700
	FileMode   fs.FileMode = 0o600
	// Version is the version of config.json this mardp writes.
	Version = 2
)

// Sound says where the sound plays (SPEC §10.1).
type Sound string

// The places the sound may play.
const (
	SoundOff    Sound = "off"
	SoundClient Sound = "client"
	SoundBoth   Sound = "both"
)

// The defaults of the settings (SPEC §6, §9.5, §10.1).
const (
	DefaultPort       = 3389
	DefaultMaxFPS     = 60
	DefaultMaxBitrate = 20_000_000
)

// Settings are what config.json holds (SPEC §12.2).
type Settings struct {
	// Version is the file's version; Save writes Version.
	Version int `json:"version"`
	// User is the user name a client logs in with.
	User string `json:"user"`
	// Password is the password's hash; nil until one is set.
	Password *PasswordHash `json:"password,omitempty"`
	// Port and Address are where the server listens; "" is every address.
	Port    int    `json:"port"`
	Address string `json:"address"`
	// StartServer starts the server when the app opens; OpenAtLogin opens
	// the app at login.
	StartServer bool `json:"startServer"`
	OpenAtLogin bool `json:"openAtLogin"`
	// Display is the UUID of the display shown; "" is the main display.
	Display string `json:"display"`
	// MaxHeight caps the desktop's height; 0 is no cap.
	MaxHeight int `json:"maxHeight"`
	// MaxFPS caps the frame rate of the H.264 path (SPEC §9.4): 30 or 60.
	MaxFPS int `json:"maxFps"`
	// MaxBitrate caps the H.264 bitrate, in bits a second.
	MaxBitrate int `json:"maxBitrate"`
	// Sound says where the sound plays.
	Sound Sound `json:"sound"`
	// SwapCtrlCommand swaps Ctrl and Command (D19).
	SwapCtrlCommand bool `json:"swapCtrlCommand"`
}

// Defaults returns the settings of a first start.
func Defaults() Settings {
	return Settings{
		Version: Version, Port: DefaultPort, MaxFPS: DefaultMaxFPS,
		MaxBitrate: DefaultMaxBitrate, Sound: SoundClient,
	}
}

// Ready says whether the server may start: a user name and a password are
// set (SPEC §8.2).
func (s Settings) Ready() bool { return s.User != "" && s.Password != nil }

// Validate checks that each setting is in its range.
func (s Settings) Validate() error {
	const msg = "a setting is out of its range"
	switch {
	case s.Port < 1 || s.Port > 65535:
		return errors.New(TagInvalid, msg, "field", "port", "value", s.Port)
	case s.Address != "" && net.ParseIP(s.Address) == nil:
		return errors.New(TagInvalid, msg, "field", "address", "value", s.Address)
	case s.MaxHeight < 0 || s.MaxHeight == 1 || s.MaxHeight > 8192:
		return errors.New(TagInvalid, msg, "field", "maxHeight", "value", s.MaxHeight)
	case s.MaxFPS != 30 && s.MaxFPS != 60:
		return errors.New(TagInvalid, msg, "field", "maxFps", "value", s.MaxFPS)
	case s.MaxBitrate < 1_000_000 || s.MaxBitrate > 1_000_000_000:
		return errors.New(TagInvalid, msg, "field", "maxBitrate", "value", s.MaxBitrate)
	case s.Sound != SoundOff && s.Sound != SoundClient && s.Sound != SoundBoth:
		return errors.New(TagInvalid, msg, "field", "sound", "value", string(s.Sound))
	}
	if s.Password != nil {
		if err := s.Password.check(); err != nil {
			return err
		}
	}
	return nil
}

// Dir returns mardp's folder: MARDP_HOME when getenv has it, otherwise
// mardp in the user's configuration folder (~/Library/Application Support
// on macOS).
func Dir(getenv func(string) string) (string, error) {
	if d := getenv(HomeEnv); d != "" {
		return filepath.Abs(d)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", errors.NewErr(TagHomeMissing, err)
	}
	return filepath.Join(base, "mardp"), nil
}

// Folder is mardp's folder.
type Folder struct {
	// Path is where it is.
	Path string
	log  *slog.Logger
	// upgrades turn a file of version i+1 into one of version i+2.
	upgrades []func(map[string]json.RawMessage) error
}

// upgrades are the upgrades of config.json, one per version after the
// first.
var upgrades = []func(map[string]json.RawMessage) error{upgradeTo2}

// upgradeTo2 upgrades version 1, whose frame-rate cap of 30 was the
// default of the bitmap paths and could not be set in the window: from
// version 2 the setting caps the H.264 path, whose default is 60 (SPEC
// §9.4), so a 30 becomes 60.
func upgradeTo2(m map[string]json.RawMessage) error {
	if string(m["maxFps"]) == "30" {
		m["maxFps"] = json.RawMessage("60")
	}
	return nil
}

// Open makes the folder at path when it is missing and sets the modes of
// the folder and its files to FolderMode and FileMode, logging each one it
// corrects.
func Open(ctx context.Context, path string, log *slog.Logger) (*Folder, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.NewErr(TagFolderFailed, err)
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(path, FolderMode); err != nil {
		return nil, errors.NewErr(TagFolderFailed, err).SetMetadata("path", path)
	}
	f := &Folder{Path: path, log: log, upgrades: upgrades}
	if err := f.fixMode(path, FolderMode); err != nil {
		return nil, err
	}
	for _, name := range []string{FileName, certFile, keyFile} {
		if err := f.fixMode(filepath.Join(path, name), FileMode); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// fixMode sets the mode of the file at p, when it exists, to mode.
func (f *Folder) fixMode(p string, mode fs.FileMode) error {
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.NewErr(TagFolderFailed, err).SetMetadata("path", p)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return errors.New(TagFolderFailed, "a file of mardp's folder is a symbolic link", "path", p)
	}
	if info.Mode().Perm() == mode {
		return nil
	}
	if err := os.Chmod(p, mode); err != nil {
		return errors.NewErr(TagFolderFailed, err).SetMetadata("path", p, "mode", mode.String())
	}
	f.log.Warn("the mode of a file of mardp's folder was corrected", "path", p, "was", info.Mode().Perm().String(), "now", mode.String())
	return nil
}

// Load reads config.json, upgrading a file of an older version; a folder
// without one gives Defaults.
func (f *Folder) Load(ctx context.Context) (Settings, error) {
	if err := ctx.Err(); err != nil {
		return Settings{}, errors.NewErr(TagReadFailed, err)
	}
	p := filepath.Join(f.Path, FileName)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Settings{}, errors.NewErr(TagReadFailed, err).SetMetadata("path", p)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Settings{}, errors.NewErr(TagCorrupt, err).SetMetadata("path", p)
	}
	var version int
	raw, ok := fields["version"]
	if !ok {
		return Settings{}, errors.New(TagVersionMissing, "config.json has no version", "path", p)
	}
	if err := json.Unmarshal(raw, &version); err != nil || version < 1 {
		return Settings{}, errors.New(TagCorrupt, "config.json's version is not a version", "path", p, "version", string(raw))
	}
	latest := len(f.upgrades) + 1
	if version > latest {
		return Settings{}, errors.NewErrf(TagTooNew,
			"config.json is from a newer mardp (version %d; this mardp reads up to %d): update mardp", version, latest)
	}
	for v := version; v < latest; v++ {
		if err := f.upgrades[v-1](fields); err != nil {
			return Settings{}, errors.NewErr(TagUpgradeFailed, err).SetMetadata("from", v, "to", v+1)
		}
		fields["version"] = json.RawMessage(strconv.Itoa(v + 1))
	}
	upgraded, err := json.Marshal(fields)
	if err != nil {
		return Settings{}, errors.NewErr(TagCorrupt, err).SetMetadata("path", p)
	}
	s := Defaults()
	if err := json.Unmarshal(upgraded, &s); err != nil {
		return Settings{}, errors.NewErr(TagCorrupt, err).SetMetadata("path", p)
	}
	if err := s.Validate(); err != nil {
		return Settings{}, err
	}
	if version < latest {
		f.log.Info("config.json was upgraded", "from", version, "to", latest)
	}
	return s, nil
}

// Save writes s to config.json, at the newest version, replacing the file
// in one step.
func (f *Folder) Save(ctx context.Context, s Settings) error {
	s.Version = len(f.upgrades) + 1
	if err := s.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return errors.NewErr(TagWriteFailed, err)
	}
	return f.write(ctx, FileName, append(data, '\n'))
}

// write replaces the file name of the folder with data: a temporary file
// of mode FileMode, synced, then renamed over it.
func (f *Folder) write(ctx context.Context, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return errors.NewErr(TagWriteFailed, err)
	}
	p := filepath.Join(f.Path, name)
	tmp, err := os.CreateTemp(f.Path, "."+name+".*")
	if err != nil {
		return errors.NewErr(TagWriteFailed, err).SetMetadata("path", p)
	}
	defer func() {
		if err := os.Remove(tmp.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			f.log.Warn("a temporary file was left in mardp's folder", "path", tmp.Name(), "err", err)
		}
	}()
	werr := fill(tmp, data)
	cerr := tmp.Close()
	if werr != nil {
		return errors.NewErr(TagWriteFailed, werr).SetMetadata("path", p)
	}
	if cerr != nil {
		return errors.NewErr(TagWriteFailed, cerr).SetMetadata("path", p)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return errors.NewErr(TagWriteFailed, err).SetMetadata("path", p)
	}
	return nil
}

// fill sets the mode of tmp, writes data to it and syncs it.
func fill(tmp *os.File, data []byte) error {
	if err := tmp.Chmod(FileMode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	return tmp.Sync()
}

// readFile reads the file name of the folder; ok is false when it does not
// exist.
func (f *Folder) readFile(name string) (data []byte, ok bool, err error) {
	p := filepath.Join(f.Path, name)
	data, err = os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.NewErr(TagReadFailed, err).SetMetadata("path", p)
	}
	return data, true, nil
}
