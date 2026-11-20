package ui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
)

// TestTutorialPictures draws the window as docs/TUTORIAL.md shows it at
// each step, into the folder $MARDP_TUTORIAL:
//
//	MARDP_TUTORIAL=$PWD/docs/pictures/tutorial go test -tags debug_mode -run TestTutorialPictures ./internal/ui
//
// Without the variable it only checks that each step's window is the one
// the tutorial describes.
func TestTutorialPictures(t *testing.T) {
	const fingerprint = "8b:3d:23:65:33:f0:1d:52:1b:4d:c9:cf:ab:73:b2:72:55:f9:85:f1:b1:78:e4:2d:ad:2a:40:71:c3:34:16:2e"
	granted := Status{Version: "v1.0.6", Settings: config.Defaults(), ScreenRecording: true, Accessibility: true}
	ready := granted
	ready.User, ready.LoginSet = "manos", true
	listening := ready
	listening.Server, listening.Addresses, listening.Fingerprint = ServerListening, []string{"192.168.1.20:3389"}, fingerprint
	at := time.Date(2026, 11, 20, 21, 30, 0, 0, time.Local)
	connected := listening
	connected.Server, connected.Video = ServerConnected, TextVideoAVC420
	connected.Session = host.SessionInfo{Client: "linux-desktop", Remote: "192.168.1.55", Since: at, Width: 1920, Height: 1080,
		Sound: "AAC", FPS: 60, Bitrate: 8_400_000, Delay: 38 * time.Millisecond}
	connected.Events = []Event{{At: at, Kind: EventConnected, Client: "linux-desktop", Remote: "192.168.1.55", User: "manos"}}
	connected.EventTotal = 1
	silent := connected
	silent.Session.Sound = ""

	steps := []struct {
		name string
		st   Status
		do   func(t *testing.T, ts *gluttertest.Tester)
		want []string
	}{
		{"01-first-start", Status{Version: "v1.0.6", Settings: config.Defaults()}, nil, []string{TextChecklist, TextScreenRecording, TextAccessibility}},
		{"02-user-and-password", granted, func(t *testing.T, ts *gluttertest.Tester) {
			ts.Tap(gluttertest.ByKey(KeyCheck(TextLogin)))
			ts.Pump()
			ts.EnterText(gluttertest.ByKey(KeyUser), "manos")
			ts.EnterText(gluttertest.ByKey(KeyPassword), "a long password")
		}, []string{TextChecklist, TextSave}},
		{"03-ready-to-start", ready, nil, []string{TextStopped, TextStart}},
		{"04-listening", listening, nil, []string{TextListening, "192.168.1.20:3389", fingerprint}},
		{"05-connected", connected, nil, []string{TextConnected, "Sound: AAC", TextVideo + TextVideoAVC420}},
		{"06-no-sound", silent, nil, []string{"Sound: " + TextSoundNone + TextSoundHint}},
	}
	dir := os.Getenv("MARDP_TUTORIAL")
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			ts, _ := newSizedTester(t, &fakeBackend{st: s.st}, m.Size{Width: WindowWidth, Height: WindowHeight})
			if s.do != nil {
				s.do(t, ts)
			}
			ts.PumpAndSettle()
			for _, text := range s.want {
				if !shows(ts, text) {
					t.Errorf("the window does not show %q", text)
				}
			}
			if dir == "" {
				return
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, ts.Screenshot()); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, s.name+".png"), buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
