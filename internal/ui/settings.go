package ui

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"

	"github.com/rm4n0s/mardp/internal/config"
	"github.com/rm4n0s/mardp/internal/host"
)

// The texts of the settings (SPEC §12.2).
const (
	TextSettings    = "Settings"
	TextAtOnce      = "These apply at once."
	TextNextSession = "These apply at the next session: a running server starts again with them as soon as no client is connected."
	TextNextOpen    = "This applies the next time the app opens."

	TextOpenAtLogin = "Open the app at login"
	// TextLoginApproval is said while macOS waits for the user to allow
	// the app to open at login.
	TextLoginApproval = "macOS waits for you to allow it: System Settings › General › Login Items, under \"Open at Login\"."
	// TextLoginFailed is said when the app could not be added to the login
	// items, or taken out.
	TextLoginFailed = "The login items could not be changed: "
	TextStartServer = "Start the server when the app opens"
	TextPort        = "Port"
	TextAddress     = "Address"
	TextAddressHelp = "Empty: every address of this Mac."
	TextDisplay     = "Display"
	TextMainDisplay = "The main display"
	TextAbsent      = "A display not connected now"
	TextMaxHeight   = "Height cap"
	TextNoCap       = "No cap"
	TextMaxFPS      = "Frame rate cap"
	TextBitrate     = "Bitrate cap (Mbit/s)"
	TextSound       = "Sound"
	TextSoundOff    = "Off"
	TextSoundClient = "On the client only"
	TextSoundBoth   = "On both the client and the Mac"
	TextSwap        = "Swap Ctrl and Command"
	TextSwapHelp    = "The client's Ctrl acts as Command on the Mac, and its Command (the Windows key) as Ctrl."

	TextNoLogin        = "None set"
	TextSaveSettings   = "Save settings"
	TextRevert         = "Undo changes"
	TextSaved          = "Saved."
	TextPending        = "Saved. The changes apply at the next session: the server starts again with them once no client is connected."
	TextNewCertificate = "New certificate"
	TextNewCertDone    = "A new certificate was made. It applies at the next session; Remmina asks to accept it again, with its new fingerprint."

	textPortInvalid    = "A port from 1 to 65535."
	textAddressInvalid = "An IP address of this Mac, or empty for every address."
	textBitrateInvalid = "From 1 to 1000 Mbit/s."
	textInvalid        = "Not a value this setting takes."
)

// The fields of the settings, as config's errors name them.
const (
	FieldPort       = "port"
	FieldAddress    = "address"
	FieldMaxHeight  = "maxHeight"
	FieldMaxFPS     = "maxFps"
	FieldMaxBitrate = "maxBitrate"
	FieldSound      = "sound"
)

// heights are the height caps offered; 0 is none.
var heights = []int{0, 2160, 1600, 1440, 1200, 1080, 900, 720}

// Form is the settings as the window edits them: the text fields as they
// are typed, the bitrate in Mbit/s.
type Form struct {
	Port, Address, Bitrate   string
	StartServer, OpenAtLogin bool
	Display                  string
	MaxHeight, MaxFPS        int
	Sound                    config.Sound
	SwapCtrlCommand          bool
}

// NewForm is the form of s.
func NewForm(s config.Settings) Form {
	return Form{
		Port: strconv.Itoa(s.Port), Address: s.Address,
		Bitrate:     strconv.FormatFloat(float64(s.MaxBitrate)/1e6, 'f', -1, 64),
		StartServer: s.StartServer, OpenAtLogin: s.OpenAtLogin, Display: s.Display,
		MaxHeight: s.MaxHeight, MaxFPS: s.MaxFPS, Sound: s.Sound, SwapCtrlCommand: s.SwapCtrlCommand,
	}
}

// Settings returns base with the form's values, or the errors of the
// fields that are not valid, by field.
func (f Form) Settings(base config.Settings) (config.Settings, map[string]string) {
	s := base
	errs := map[string]string{}
	s.StartServer, s.OpenAtLogin, s.Display = f.StartServer, f.OpenAtLogin, f.Display
	s.MaxHeight, s.MaxFPS, s.Sound, s.SwapCtrlCommand = f.MaxHeight, f.MaxFPS, f.Sound, f.SwapCtrlCommand
	s.Address = strings.TrimSpace(f.Address)
	if p, err := strconv.Atoi(strings.TrimSpace(f.Port)); err == nil {
		s.Port = p
	} else {
		errs[FieldPort] = textPortInvalid
	}
	if b, err := strconv.ParseFloat(strings.TrimSpace(f.Bitrate), 64); err == nil && !math.IsNaN(b) && !math.IsInf(b, 0) && b < 1e4 {
		s.MaxBitrate = int(math.Round(b * 1e6))
	} else {
		errs[FieldMaxBitrate] = textBitrateInvalid
	}
	if len(errs) == 0 {
		if field, ok := FieldOf(s.Validate()); ok {
			errs[field] = FieldMessage(field)
		}
	}
	if len(errs) > 0 {
		return base, errs
	}
	return s, nil
}

// FieldOf is the field an error of the settings names in its metadata
// ("field"), as config's TagInvalid does; ok is false for other errors.
func FieldOf(err error) (field string, ok bool) {
	e, isErr := errors.FromError(err)
	if !isErr {
		return "", false
	}
	field, ok = e.Metadata["field"].(string)
	return field, ok
}

// FieldMessage is what the window says under a field that is not valid.
func FieldMessage(field string) string {
	switch field {
	case FieldPort:
		return textPortInvalid
	case FieldAddress:
		return textAddressInvalid
	case FieldMaxBitrate:
		return textBitrateInvalid
	}
	return textInvalid
}

// Choice is an item of a setting's menu.
type Choice[T comparable] struct {
	Value T
	Text  string
}

// DisplayChoices are the choices of the display: the main display, then
// each display, then current when no display has it.
func DisplayChoices(displays []host.Display, current string) []Choice[string] {
	cs := []Choice[string]{{"", TextMainDisplay}}
	found := current == ""
	for _, d := range displays {
		text := fmt.Sprintf("%s, %d×%d", d.Name, d.Width, d.Height)
		if d.Main {
			text += " (main)"
		}
		cs = append(cs, Choice[string]{d.ID, text})
		found = found || d.ID == current
	}
	if !found {
		cs = append(cs, Choice[string]{current, TextAbsent})
	}
	return cs
}

// HeightChoices are the choices of the height cap, with current among
// them.
func HeightChoices(current int) []Choice[int] {
	hs := slices.Clone(heights)
	if !slices.Contains(hs, current) {
		hs = append(hs, current)
		slices.SortFunc(hs[1:], func(a, b int) int { return b - a })
	}
	cs := make([]Choice[int], len(hs))
	for i, h := range hs {
		cs[i] = Choice[int]{h, strconv.Itoa(h) + " pixels"}
		if h == 0 {
			cs[i].Text = TextNoCap
		}
	}
	return cs
}

// FPSChoices are the choices of the frame rate cap, with current among
// them.
func FPSChoices(current int) []Choice[int] {
	cs := []Choice[int]{{60, "60 frames a second"}, {30, "30 frames a second"}}
	if current != 60 && current != 30 {
		cs = append(cs, Choice[int]{current, strconv.Itoa(current) + " frames a second"})
	}
	return cs
}

// SoundChoices are the choices of where the sound plays, with current
// among them.
func SoundChoices(current config.Sound) []Choice[config.Sound] {
	cs := []Choice[config.Sound]{
		{config.SoundClient, TextSoundClient}, {config.SoundBoth, TextSoundBoth}, {config.SoundOff, TextSoundOff},
	}
	if !slices.ContainsFunc(cs, func(c Choice[config.Sound]) bool { return c.Value == current }) {
		cs = append(cs, Choice[config.Sound]{current, textInvalid})
	}
	return cs
}
