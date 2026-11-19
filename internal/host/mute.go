package host

import (
	"context"
	"log/slog"
	"sync"
)

// muter mutes the Mac's output while sessions get the sound, so that it
// plays on the client only (SPEC §10.1). It mutes the default output
// device when the first session's sound starts, unless it is muted
// already, and unmutes that device when the last one's ends. A marker
// records the device while it is muted, so that RecoverOutput can unmute
// it after a crash. It never touches a device it did not mute, nor a
// volume.
type muter struct {
	out    Output
	marker MuteMarker
	log    *slog.Logger

	mu sync.Mutex
	// n counts the sessions holding the mute; muted is the UID of the
	// device muted for them, "" when none was.
	n     int
	muted string
}

// hold mutes the output, when this is the first hold, until release is
// called.
func (m *muter) hold(ctx context.Context) (release func()) {
	if m.out == nil {
		return func() {}
	}
	ctx = context.WithoutCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	if m.n == 1 {
		m.mute(ctx)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.n--
			if m.n == 0 {
				m.unmute(ctx)
			}
		})
	}
}

// mute mutes the default output device, unless it is muted already, after
// marking it.
func (m *muter) mute(ctx context.Context) {
	uid, err := m.out.DefaultOutput()
	if err != nil {
		m.log.Warn("the sound plays on the Mac too: no default output", "err", err)
		return
	}
	muted, err := m.out.OutputMuted(uid)
	if err != nil {
		m.log.Warn("the sound plays on the Mac too: the output cannot be muted", "device", uid, "err", err)
		return
	}
	if muted {
		m.log.Info("the Mac's output is muted already", "device", uid)
		return
	}
	if m.marker != nil {
		if err := m.marker.MarkMuted(ctx, uid); err != nil {
			m.log.Warn("the sound plays on the Mac too: the mute could not be recorded", "device", uid, "err", err)
			return
		}
	}
	if err := m.out.SetOutputMuted(uid, true); err != nil {
		m.log.Warn("the sound plays on the Mac too: the output cannot be muted", "device", uid, "err", err)
		m.clear(ctx)
		return
	}
	m.muted = uid
	m.log.Info("the Mac's output is muted while the client plays the sound", "device", uid)
}

// unmute unmutes the device mute muted, and clears the marker; a device
// that cannot be unmuted keeps its marker, for the next start.
func (m *muter) unmute(ctx context.Context) {
	if m.muted == "" {
		return
	}
	uid := m.muted
	m.muted = ""
	if err := m.out.SetOutputMuted(uid, false); err != nil {
		m.log.Warn("the Mac's output could not be unmuted", "device", uid, "err", err)
		return
	}
	m.clear(ctx)
	m.log.Info("the Mac's output is unmuted", "device", uid)
}

// clear removes the marker.
func (m *muter) clear(ctx context.Context) {
	if m.marker == nil {
		return
	}
	if err := m.marker.ClearMuted(ctx); err != nil {
		m.log.Warn("the record of the muted output could not be removed", "err", err)
	}
}

// RecoverOutput unmutes the output device a mardp that ended without
// unmuting it left marked (SPEC §10.1), and removes the marker. A device
// that cannot be unmuted, such as one unplugged, keeps its marker for the
// next start. It is called at start, before any session.
func RecoverOutput(ctx context.Context, out Output, marker MuteMarker, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	uid, ok, err := marker.MutedMark(ctx)
	if err != nil {
		log.Warn("the record of the muted output could not be read", "err", err)
		return
	}
	if !ok {
		return
	}
	if err := out.SetOutputMuted(uid, false); err != nil {
		log.Warn("the output mardp muted before could not be unmuted", "device", uid, "err", err)
		return
	}
	if err := marker.ClearMuted(ctx); err != nil {
		log.Warn("the record of the muted output could not be removed", "err", err)
		return
	}
	log.Info("unmuted the output mardp left muted", "device", uid)
}
