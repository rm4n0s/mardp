package ui

import "testing"

// TestOpenProblems: the window says why the server did not start as the app
// opened, that macOS waits for the user to allow the login item, and why
// the login items could not be changed; and none of it once it is over
// (SPEC §7.3).
func TestOpenProblems(t *testing.T) {
	st := settingsStatus()
	st.Settings.OpenAtLogin = true
	st.StartProblem, st.LoginApproval, st.LoginProblem = "the port is taken", true, "Operation not permitted"
	b := &fakeBackend{st: st}
	ts := newSettingsTester(t, b)
	texts := []string{TextStartFailed + "the port is taken", TextLoginApproval, TextLoginFailed + "Operation not permitted"}
	for _, l := range texts {
		if !shows(ts, l) {
			t.Errorf("the window does not show %q", l)
		}
	}
	b.set(func(st *Status) { st.StartProblem, st.LoginApproval, st.LoginProblem = "", false, "" })
	ts.Pump(PollInterval)
	for _, l := range texts {
		if shows(ts, l) {
			t.Errorf("the window still shows %q", l)
		}
	}
}
