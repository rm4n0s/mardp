package ui

import (
	"slices"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
)

// testDisplays are the Mac's displays in the tests.
var testDisplays = []host.Display{
	{ID: "1", Name: "Built-in Retina Display", Width: 3024, Height: 1964, Main: true},
	{ID: "2", Name: "Studio Display", Width: 5120, Height: 2880},
}

// settingsStatus is ready() with the default settings and two displays.
func settingsStatus() Status {
	st := ready()
	st.Settings = config.Defaults()
	st.Settings.User = st.User
	st.Displays = slices.Clone(testDisplays)
	return st
}

// newSettingsTester is a window tall enough to show the whole settings
// card.
func newSettingsTester(t *testing.T, b *fakeBackend) *gluttertest.Tester {
	t.Helper()
	ts := gluttertest.New(t, gluttertest.Opts{Size: m.Size{Width: 560, Height: 2400}})
	ts.PumpWidget(NewApp(b))
	return ts
}

// fieldText is the text of the field with key k.
func fieldText(t *testing.T, ts *gluttertest.Tester, k m.ValueKey[string]) string {
	t.Helper()
	f, ok := ts.Widget(gluttertest.ByKey(k)).(*m.TextField)
	if !ok {
		t.Fatalf("%v is not a text field", k)
	}
	return f.Controller().Text()
}

// pick chooses text in the menu with key k.
func pick(ts *gluttertest.Tester, k m.ValueKey[string], text string) {
	ts.Tap(gluttertest.ByKey(k))
	ts.PumpAndSettle()
	ts.Tap(gluttertest.Last(gluttertest.ByText(text)))
	ts.PumpAndSettle()
}

func TestForm(t *testing.T) {
	base := config.Defaults()
	if s, errs := NewForm(base).Settings(base); errs != nil || s != base {
		t.Fatalf("the form of the defaults: %v, %+v", errs, s)
	}
	for _, tt := range []struct {
		name  string
		edit  func(f *Form)
		want  func(s *config.Settings)
		field string
	}{
		{"port", func(f *Form) { f.Port = " 4000 " }, func(s *config.Settings) { s.Port = 4000 }, ""},
		{"port not a number", func(f *Form) { f.Port = "abc" }, nil, FieldPort},
		{"port 0", func(f *Form) { f.Port = "0" }, nil, FieldPort},
		{"port too big", func(f *Form) { f.Port = "65536" }, nil, FieldPort},
		{"address", func(f *Form) { f.Address = "10.0.0.2 " }, func(s *config.Settings) { s.Address = "10.0.0.2" }, ""},
		{"address IPv6", func(f *Form) { f.Address = "fd00::2" }, func(s *config.Settings) { s.Address = "fd00::2" }, ""},
		{"address not an IP", func(f *Form) { f.Address = "my-mac.local" }, nil, FieldAddress},
		{"bitrate", func(f *Form) { f.Bitrate = "12.5" }, func(s *config.Settings) { s.MaxBitrate = 12_500_000 }, ""},
		{"bitrate 1000", func(f *Form) { f.Bitrate = "1000" }, func(s *config.Settings) { s.MaxBitrate = 1_000_000_000 }, ""},
		{"bitrate too small", func(f *Form) { f.Bitrate = "0.5" }, nil, FieldMaxBitrate},
		{"bitrate too big", func(f *Form) { f.Bitrate = "1001" }, nil, FieldMaxBitrate},
		{"bitrate huge", func(f *Form) { f.Bitrate = "1e300" }, nil, FieldMaxBitrate},
		{"bitrate NaN", func(f *Form) { f.Bitrate = "NaN" }, nil, FieldMaxBitrate},
		{"bitrate not a number", func(f *Form) { f.Bitrate = "fast" }, nil, FieldMaxBitrate},
		{"height", func(f *Form) { f.MaxHeight = 1080 }, func(s *config.Settings) { s.MaxHeight = 1080 }, ""},
		{"height 1", func(f *Form) { f.MaxHeight = 1 }, nil, FieldMaxHeight},
		{"fps", func(f *Form) { f.MaxFPS = 30 }, func(s *config.Settings) { s.MaxFPS = 30 }, ""},
		{"fps 50", func(f *Form) { f.MaxFPS = 50 }, nil, FieldMaxFPS},
		{"sound", func(f *Form) { f.Sound = config.SoundOff }, func(s *config.Settings) { s.Sound = config.SoundOff }, ""},
		{"sound unknown", func(f *Form) { f.Sound = "loud" }, nil, FieldSound},
		{"display", func(f *Form) { f.Display = "2" }, func(s *config.Settings) { s.Display = "2" }, ""},
		{"swap", func(f *Form) { f.SwapCtrlCommand = true }, func(s *config.Settings) { s.SwapCtrlCommand = true }, ""},
		{"start server", func(f *Form) { f.StartServer = !base.StartServer }, func(s *config.Settings) { s.StartServer = !base.StartServer }, ""},
		{"open at login", func(f *Form) { f.OpenAtLogin = !base.OpenAtLogin }, func(s *config.Settings) { s.OpenAtLogin = !base.OpenAtLogin }, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := NewForm(base)
			tt.edit(&f)
			s, errs := f.Settings(base)
			if tt.field != "" {
				if len(errs) != 1 || errs[tt.field] != FieldMessage(tt.field) {
					t.Errorf("errors %v, want one for %s", errs, tt.field)
				}
				if s != base {
					t.Errorf("settings %+v with errors", s)
				}
				return
			}
			want := base
			tt.want(&want)
			if errs != nil || s != want {
				t.Errorf("%+v, %v; want %+v", s, errs, want)
			}
			if NewForm(s) != f && tt.name != "port" && tt.name != "address" {
				t.Errorf("the form of the settings is %+v, want %+v", NewForm(s), f)
			}
		})
	}
}

func TestFieldOf(t *testing.T) {
	s := config.Defaults()
	s.Port = 0
	if field, ok := FieldOf(s.Validate()); !ok || field != FieldPort {
		t.Errorf("config's error: %q, %v", field, ok)
	}
	if _, ok := FieldOf(errors.New("TestOther", "other")); ok {
		t.Error("an error without a field has one")
	}
	if _, ok := FieldOf(nil); ok {
		t.Error("no error has a field")
	}
	if FieldMessage("swapCtrlCommand") != textInvalid {
		t.Error("an unknown field's message")
	}
}

func TestChoices(t *testing.T) {
	values := func(cs []Choice[string]) []string {
		var v []string
		for _, c := range cs {
			v = append(v, c.Value)
		}
		return v
	}
	if got := values(DisplayChoices(testDisplays, "2")); !slices.Equal(got, []string{"", "1", "2"}) {
		t.Errorf("displays %v", got)
	}
	cs := DisplayChoices(testDisplays, "9")
	if got := values(cs); !slices.Equal(got, []string{"", "1", "2", "9"}) || cs[3].Text != TextAbsent {
		t.Errorf("a saved display not connected: %+v", cs)
	}
	if cs[1].Text != "Built-in Retina Display, 3024×1964 (main)" || cs[2].Text != "Studio Display, 5120×2880" {
		t.Errorf("display texts %+v", cs)
	}
	var hs []int
	for _, c := range HeightChoices(1234) {
		hs = append(hs, c.Value)
	}
	if !slices.Equal(hs, []int{0, 2160, 1600, 1440, 1234, 1200, 1080, 900, 720}) {
		t.Errorf("heights %v", hs)
	}
	if HeightChoices(1080)[0].Text != TextNoCap || len(HeightChoices(1080)) != len(heights) {
		t.Errorf("heights %+v", HeightChoices(1080))
	}
	if len(FPSChoices(60)) != 2 || len(FPSChoices(0)) != 3 {
		t.Error("the frame rates")
	}
	if len(SoundChoices(config.SoundBoth)) != 3 || len(SoundChoices("")) != 4 {
		t.Error("the sounds")
	}
}

// TestSettingsSave changes each setting in the window and saves it: the
// backend gets the settings with that one changed.
func TestSettingsSave(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(ts *gluttertest.Tester)
		want   func(s *config.Settings)
	}{
		{"port", func(ts *gluttertest.Tester) { ts.EnterText(gluttertest.ByKey(KeyPort), "4000") },
			func(s *config.Settings) { s.Port = 4000 }},
		{"address", func(ts *gluttertest.Tester) { ts.EnterText(gluttertest.ByKey(KeyAddress), "192.168.1.20") },
			func(s *config.Settings) { s.Address = "192.168.1.20" }},
		{"bitrate", func(ts *gluttertest.Tester) { ts.EnterText(gluttertest.ByKey(KeyBitrate), "12.5") },
			func(s *config.Settings) { s.MaxBitrate = 12_500_000 }},
		{"display", func(ts *gluttertest.Tester) { pick(ts, KeyDisplay, "Studio Display, 5120×2880") },
			func(s *config.Settings) { s.Display = "2" }},
		{"height", func(ts *gluttertest.Tester) { pick(ts, KeyMaxHeight, "1080 pixels") },
			func(s *config.Settings) { s.MaxHeight = 1080 }},
		{"fps", func(ts *gluttertest.Tester) { pick(ts, KeyMaxFPS, "30 frames a second") },
			func(s *config.Settings) { s.MaxFPS = 30 }},
		{"sound", func(ts *gluttertest.Tester) { pick(ts, KeySound, TextSoundBoth) },
			func(s *config.Settings) { s.Sound = config.SoundBoth }},
		{"swap", func(ts *gluttertest.Tester) { ts.Tap(gluttertest.ByKey(KeySwap)) },
			func(s *config.Settings) { s.SwapCtrlCommand = true }},
		{"open at login", func(ts *gluttertest.Tester) { ts.Tap(gluttertest.ByKey(KeyOpenAtLogin)) },
			func(s *config.Settings) { s.OpenAtLogin = !s.OpenAtLogin }},
		{"start server", func(ts *gluttertest.Tester) { ts.Tap(gluttertest.ByKey(KeyStartServer)) },
			func(s *config.Settings) { s.StartServer = !s.StartServer }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &fakeBackend{st: settingsStatus()}
			ts := newSettingsTester(t, b)
			if enabled(t, ts, KeySaveSettings) || enabled(t, ts, KeyRevert) {
				t.Fatal("Save or Undo can be pressed without changes")
			}
			tt.change(ts)
			ts.PumpAndSettle()
			if !enabled(t, ts, KeySaveSettings) || !enabled(t, ts, KeyRevert) {
				t.Fatal("Save or Undo cannot be pressed after a change")
			}
			ts.Tap(gluttertest.ByKey(KeySaveSettings))
			settle(t, ts, "saved", func() bool { return shows(ts, TextSaved) })
			want := settingsStatus().Settings
			tt.want(&want)
			if got := b.savedSettings(); len(got) != 1 || got[0] != want {
				t.Errorf("saved %+v, want %+v", got, want)
			}
			if enabled(t, ts, KeySaveSettings) {
				t.Error("Save can be pressed after saving")
			}
			if shows(ts, TextPending) {
				t.Error("pending with the server stopped")
			}
		})
	}
}

// TestSettingsInvalid: a setting that is not valid is not saved, and the
// window says why under it.
func TestSettingsInvalid(t *testing.T) {
	for _, tt := range []struct {
		name    string
		key     m.ValueKey[string]
		text    string
		message string
	}{
		{"port", KeyPort, "65536", textPortInvalid},
		{"port not a number", KeyPort, "x", textPortInvalid},
		{"address", KeyAddress, "my-mac", textAddressInvalid},
		{"bitrate", KeyBitrate, "2000", textBitrateInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &fakeBackend{st: settingsStatus()}
			ts := newSettingsTester(t, b)
			ts.EnterText(gluttertest.ByKey(tt.key), tt.text)
			ts.PumpAndSettle()
			ts.Tap(gluttertest.ByKey(KeySaveSettings))
			ts.PumpAndSettle()
			if !shows(ts, tt.message) {
				t.Errorf("the window does not say %q", tt.message)
			}
			if got := b.savedSettings(); len(got) != 0 {
				t.Errorf("saved %+v", got)
			}
			// Undo puts back the saved settings and clears the message.
			ts.Tap(gluttertest.ByKey(KeyRevert))
			ts.PumpAndSettle()
			if shows(ts, tt.message) || enabled(t, ts, KeySaveSettings) {
				t.Error("Undo leaves the message, or a change")
			}
			if got, want := fieldText(t, ts, tt.key), NewForm(config.Defaults()); got != want.Port && got != want.Address && got != want.Bitrate {
				t.Errorf("Undo leaves %q", got)
			}
		})
	}
}

// TestSettingsRefused: the backend refuses a setting (an address not of
// this Mac), or fails otherwise.
func TestSettingsRefused(t *testing.T) {
	b := &fakeBackend{st: settingsStatus(), saveErr: errors.New("TestAddressNotLocal", "not of this Mac", "field", FieldAddress)}
	ts := newSettingsTester(t, b)
	ts.EnterText(gluttertest.ByKey(KeyAddress), "10.9.9.9")
	ts.PumpAndSettle()
	ts.Tap(gluttertest.ByKey(KeySaveSettings))
	settle(t, ts, "refused", func() bool { return shows(ts, textAddressInvalid) })

	b.mu.Lock()
	b.saveErr = errors.New("TestDiskFull", "the disk is full")
	b.mu.Unlock()
	ts.Tap(gluttertest.ByKey(KeySaveSettings))
	settle(t, ts, "failed", func() bool { return ts.Exists(gluttertest.ByKey(KeySettingsErr)) })
	if !shows(ts, "the disk is full") || shows(ts, TextSaved) {
		t.Error("the window does not say why the save failed")
	}
}

// TestSettingsPending: with the server running, saved settings wait for
// the next session, and the window says so until the server started again.
func TestSettingsPending(t *testing.T) {
	st := settingsStatus()
	st.Server, st.Addresses = ServerConnected, []string{"10.0.0.1:3389"}
	b := &fakeBackend{st: st}
	ts := newSettingsTester(t, b)
	for _, text := range []string{TextAtOnce, TextNextSession, TextNextOpen} {
		if !shows(ts, text) {
			t.Errorf("the window does not say %q", text)
		}
	}
	pick(ts, KeyMaxFPS, "30 frames a second")
	ts.Tap(gluttertest.ByKey(KeySaveSettings))
	settle(t, ts, "pending", func() bool { return shows(ts, TextPending) })
	b.set(func(st *Status) { st.Server, st.Pending = ServerListening, false })
	ts.Pump(PollInterval)
	if shows(ts, TextPending) || !shows(ts, TextSaved) {
		t.Error("still pending after the server started again")
	}
}

// TestSettingsFollow: settings saved elsewhere show in the form, unless it
// has changes of its own.
func TestSettingsFollow(t *testing.T) {
	b := &fakeBackend{st: settingsStatus()}
	ts := newSettingsTester(t, b)
	b.set(func(st *Status) { st.Settings.Port = 4001 })
	ts.Pump(PollInterval)
	if fieldText(t, ts, KeyPort) != "4001" || enabled(t, ts, KeySaveSettings) {
		t.Error("the form does not follow the saved settings")
	}
	ts.EnterText(gluttertest.ByKey(KeyBitrate), "7")
	ts.PumpAndSettle()
	b.set(func(st *Status) { st.Settings.Port = 4002 })
	ts.Pump(PollInterval)
	if fieldText(t, ts, KeyPort) != "4001" || fieldText(t, ts, KeyBitrate) != "7" {
		t.Error("a change in the form was lost")
	}
}

func TestSettingsNewCertificate(t *testing.T) {
	b := &fakeBackend{st: settingsStatus()}
	ts := newSettingsTester(t, b)
	ts.Tap(gluttertest.ByKey(KeyNewCertificate))
	settle(t, ts, "the certificate", func() bool { return shows(ts, TextNewCertDone) })
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.certs != 1 {
		t.Errorf("%d certificates", b.certs)
	}
}

// TestSettingsLogin: the settings card changes the login, which applies at
// once.
func TestSettingsLogin(t *testing.T) {
	b := &fakeBackend{st: settingsStatus()}
	ts := newSettingsTester(t, b)
	if shows(ts, TextChecklist) || !shows(ts, "alice") {
		t.Fatal("the settings do not show the user name")
	}
	ts.Tap(gluttertest.ByKey(KeyChangeLogin))
	ts.PumpAndSettle()
	ts.EnterText(gluttertest.ByKey(KeyUser), "bob")
	ts.EnterText(gluttertest.ByKey(KeyPassword), "a long password")
	ts.Tap(gluttertest.ByKey(KeySave))
	settle(t, ts, "the login", func() bool { return !ts.Exists(gluttertest.ByKey(KeyPassword)) })
	if _, _, _, logins := b.calls(); !slices.Equal(logins, []string{"bob:a long password"}) {
		t.Errorf("logins %q", logins)
	}
	if !shows(ts, "bob") {
		t.Error("the settings do not show the new user name")
	}
}

// TestSettingsUnknownValues: settings the menus do not offer (an old or
// hand-edited config.json) show as they are.
func TestSettingsUnknownValues(t *testing.T) {
	st := settingsStatus()
	st.Settings.MaxHeight, st.Settings.Display = 1234, "9"
	b := &fakeBackend{st: st}
	ts := newSettingsTester(t, b)
	for _, text := range []string{"1234 pixels", TextAbsent} {
		if !shows(ts, text) {
			t.Errorf("the window does not show %q", text)
		}
	}
}
