//go:build darwin

package system

import "testing"

// fakeAudio is Core Audio with two devices, 1 "Speakers" and 2 "HDMI",
// whose calls the test can make fail; HDMI has no mute.
type fakeAudio struct {
	defErr, defID    int
	uidErr, devErr   int
	mutedErr, setErr int
	muted            map[uint32]bool
}

func (f *fakeAudio) audio() coreAudio {
	names := map[uint32]string{1: "Speakers", 2: "HDMI"}
	return coreAudio{
		defaultOutput: func() (uint32, int) { return uint32(f.defID), f.defErr },
		uid:           func(id uint32) (string, int) { return names[id], f.uidErr },
		device: func(uid string) (uint32, int) {
			for id, n := range names {
				if n == uid {
					return id, f.devErr
				}
			}
			return 0, f.devErr
		},
		muted: func(id uint32) (bool, int) {
			if id == 2 {
				return false, noMute
			}
			return f.muted[id], f.mutedErr
		},
		setMuted: func(id uint32, m bool) int {
			if id == 2 {
				return noMute
			}
			if f.setErr != 0 {
				return f.setErr
			}
			f.muted[id] = m
			return 0
		},
	}
}

func TestOutput(t *testing.T) {
	f := &fakeAudio{defID: 1, muted: map[uint32]bool{}}
	s := &System{audio: f.audio()}
	uid, err := s.DefaultOutput()
	if err != nil || uid != "Speakers" {
		t.Fatalf("DefaultOutput = %q, %v", uid, err)
	}
	for _, m := range []bool{true, false} {
		if err := s.SetOutputMuted(uid, m); err != nil {
			t.Fatal(err)
		}
		if got, err := s.OutputMuted(uid); err != nil || got != m {
			t.Fatalf("OutputMuted = %v, %v; want %v", got, err, m)
		}
	}
}

func TestOutputFails(t *testing.T) {
	tests := []struct {
		name  string
		fake  fakeAudio
		run   func(s *System) error
		route string
	}{
		{"no default", fakeAudio{defErr: -50}, func(s *System) error {
			_, err := s.DefaultOutput()
			return err
		}, "System.DefaultOutput.OutputFailed"},
		{"default 0", fakeAudio{}, func(s *System) error {
			_, err := s.DefaultOutput()
			return err
		}, "System.DefaultOutput.OutputFailed"},
		{"no UID", fakeAudio{defID: 1, uidErr: -50}, func(s *System) error {
			_, err := s.DefaultOutput()
			return err
		}, "System.DefaultOutput.OutputFailed"},
		{"read: unknown", fakeAudio{}, func(s *System) error {
			_, err := s.OutputMuted("USB")
			return err
		}, "System.OutputMuted->System.device.OutputUnknown"},
		{"read: lookup", fakeAudio{devErr: -50}, func(s *System) error {
			_, err := s.OutputMuted("Speakers")
			return err
		}, "System.OutputMuted->System.device.OutputFailed"},
		{"read: no mute", fakeAudio{}, func(s *System) error {
			_, err := s.OutputMuted("HDMI")
			return err
		}, "System.OutputMuted.OutputMuteUnsupported"},
		{"read: fails", fakeAudio{mutedErr: -50}, func(s *System) error {
			_, err := s.OutputMuted("Speakers")
			return err
		}, "System.OutputMuted.OutputFailed"},
		{"set: unknown", fakeAudio{}, func(s *System) error { return s.SetOutputMuted("USB", true) },
			"System.SetOutputMuted->System.device.OutputUnknown"},
		{"set: no mute", fakeAudio{}, func(s *System) error { return s.SetOutputMuted("HDMI", true) },
			"System.SetOutputMuted.OutputMuteUnsupported"},
		{"set: fails", fakeAudio{setErr: -50}, func(s *System) error { return s.SetOutputMuted("Speakers", true) },
			"System.SetOutputMuted.OutputFailed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fake
			f.muted = map[uint32]bool{}
			assertRoute(t, tt.run(&System{audio: f.audio()}), tt.route)
		})
	}
}
