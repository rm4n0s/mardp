package ui

import (
	"context"

	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/mardp/internal/config"
)

// The keys of the settings card's fields and buttons, which the tests find
// them by.
var (
	KeyChangeLogin    = m.NewValueKey("settings change login")
	KeyOpenAtLogin    = m.NewValueKey("settings open at login")
	KeyPort           = m.NewValueKey("settings port")
	KeyAddress        = m.NewValueKey("settings address")
	KeyDisplay        = m.NewValueKey("settings display")
	KeyMaxHeight      = m.NewValueKey("settings max height")
	KeyMaxFPS         = m.NewValueKey("settings max fps")
	KeyBitrate        = m.NewValueKey("settings bitrate")
	KeySound          = m.NewValueKey("settings sound")
	KeySwap           = m.NewValueKey("settings swap")
	KeyClipboard      = m.NewValueKey("settings clipboard")
	KeyNewCertificate = m.NewValueKey("settings new certificate")
	KeyStartServer    = m.NewValueKey("settings start server")
	KeySaveSettings   = m.NewValueKey("settings save")
	KeyRevert         = m.NewValueKey("settings revert")
	KeySettingsNote   = m.NewValueKey("settings note")
	KeySettingsErr    = m.NewValueKey("settings error")
)

// settingsForm is the state of the settings card: the form, the settings
// it was made of, and what the last save said.
type settingsForm struct {
	form  Form
	saved config.Settings
	// errs are the fields that are not valid, by field; err is why the
	// last action failed otherwise, and note what it did.
	errs      map[string]string
	err, note string
	port      *m.TextEditingController
	address   *m.TextEditingController
	bitrate   *m.TextEditingController
}

func (f *settingsForm) init(s config.Settings) {
	f.port = m.NewTextEditingController()
	f.address = m.NewTextEditingController()
	f.bitrate = m.NewTextEditingController()
	f.load(s)
}

func (f *settingsForm) dispose() {
	f.port.Dispose()
	f.address.Dispose()
	f.bitrate.Dispose()
}

// load makes the form of s.
func (f *settingsForm) load(s config.Settings) {
	f.saved, f.form, f.errs = s, NewForm(s), nil
	f.port.SetText(f.form.Port)
	f.address.SetText(f.form.Address)
	f.bitrate.SetText(f.form.Bitrate)
}

// dirty says that the form has changes not saved.
func (f *settingsForm) dirty() bool { return f.form != NewForm(f.saved) }

// follow shows saved settings that changed elsewhere, unless the form has
// changes of its own.
func (f *settingsForm) follow(s config.Settings) {
	if s != f.saved && !f.dirty() {
		f.load(s)
	}
}

// edit changes the form.
func (s *windowState) edit(change func(f *Form)) {
	s.SetState(func() {
		change(&s.settings.form)
		s.settings.note, s.settings.err = "", ""
	})
}

func (s *windowState) saveSettings() {
	f := &s.settings
	set, errs := f.form.Settings(f.saved)
	if errs != nil {
		s.SetState(func() { f.errs, f.note, f.err = errs, "", "" })
		return
	}
	s.do(func(ctx context.Context) error { return s.Widget().b.SaveSettings(ctx, set) }, func(err error) {
		f.note, f.err = "", ""
		if err != nil {
			if field, ok := FieldOf(err); ok {
				f.errs = map[string]string{field: FieldMessage(field)}
			} else {
				f.err = err.Error()
			}
			return
		}
		f.load(s.Widget().b.Status().Settings)
		f.note = TextSaved
	})
}

func (s *windowState) revertSettings() {
	s.SetState(func() {
		s.settings.load(s.settings.saved)
		s.settings.note, s.settings.err = "", ""
	})
}

func (s *windowState) newCertificate() {
	f := &s.settings
	s.do(s.Widget().b.NewCertificate, func(err error) {
		f.note, f.err = "", ""
		if err != nil {
			f.err = err.Error()
			return
		}
		f.note = TextNewCertDone
	})
}

// settingsCard is the settings, grouped by when they apply (SPEC §12.2). A
// wide card has its groups in two columns, and the login's form in the
// place of the login's line.
func (s *windowState) settingsCard(bc m.BuildContext, vm ViewModel, wide bool) m.Widget {
	theme := m.ThemeOf(bc)
	f := &s.settings
	idle := !s.busy
	header := func(text string) m.Widget {
		return m.NewText(text, m.TextOpts{Style: theme.TextTheme.TitleSmall})
	}
	field := func(key m.ValueKey[string], label string, c *m.TextEditingController, name, help string, set func(f *Form, v string)) m.Widget {
		d := m.InputDecorationOpts{LabelText: m.Some(label)}
		if help != "" {
			d.HelperText = m.Some(help)
		}
		if e, ok := f.errs[name]; ok {
			d.ErrorText = m.Some(e)
		}
		dec := m.NewInputDecoration(d)
		return m.NewTextField(m.TextFieldOpts{
			Key: key, Controller: c, Decoration: &dec, Enabled: m.Some(idle),
			OnChanged: func(v string) { s.edit(func(f *Form) { set(f, v) }) },
		})
	}
	toggle := func(key m.ValueKey[string], title, subtitle string, value bool, set func(f *Form, v bool)) m.Widget {
		var on func(bool)
		if idle {
			on = func(v bool) { s.edit(func(f *Form) { set(f, v) }) }
		}
		o := m.SwitchListTileOpts{Key: key, Title: m.NewText(title), ContentPadding: flush}
		if subtitle != "" {
			o.Subtitle = m.NewText(subtitle)
		}
		return m.NewSwitchListTile(value, on, o)
	}
	label := theme.TextTheme.BodyLarge
	var onChange func()
	if idle && !s.editing {
		onChange = s.editLogin
	}
	user := s.status.User
	if user == "" {
		user = TextNoLogin
	}

	// At once.
	atOnce := []m.Widget{header(TextAtOnce)}
	if wide && s.editing {
		atOnce = append(atOnce, s.loginForm(bc, true))
	} else {
		atOnce = append(atOnce, m.NewListTile(m.ListTileOpts{
			ContentPadding: flush,
			Title:          m.NewText(TextLogin),
			Subtitle:       m.NewText(user),
			Trailing:       m.NewTextButton(onChange, m.NewText(TextChangeLogin), m.TextButtonOpts{Key: KeyChangeLogin}),
		}))
	}
	atOnce = append(atOnce, toggle(KeyOpenAtLogin, TextOpenAtLogin, "", f.form.OpenAtLogin, func(f *Form, v bool) { f.OpenAtLogin = v }))
	if s.status.LoginApproval {
		atOnce = append(atOnce, m.NewText(TextLoginApproval, m.TextOpts{Style: theme.TextTheme.BodySmall}))
	}
	if s.status.LoginProblem != "" {
		atOnce = append(atOnce, errorText(bc, TextLoginFailed+s.status.LoginProblem))
	}

	// At the next session: where the server listens, then what it sends.
	listens := []m.Widget{
		field(KeyPort, TextPort, f.port, FieldPort, "", func(f *Form, v string) { f.Port = v }),
		field(KeyAddress, TextAddress, f.address, FieldAddress, TextAddressHelp, func(f *Form, v string) { f.Address = v }),
		choice(KeyDisplay, label, TextDisplay, DisplayChoices(s.status.Displays, f.form.Display), f.form.Display, idle,
			func(v string) { s.edit(func(f *Form) { f.Display = v }) }),
		choice(KeyMaxHeight, label, TextMaxHeight, HeightChoices(f.form.MaxHeight), f.form.MaxHeight, idle,
			func(v int) { s.edit(func(f *Form) { f.MaxHeight = v }) }),
	}
	if e, ok := f.errs[FieldMaxHeight]; ok {
		listens = append(listens, errorText(bc, e))
	}
	listens = append(listens,
		toggle(KeyClipboard, TextClipboard, "", f.form.Clipboard, func(f *Form, v bool) { f.Clipboard = v }))
	sends := []m.Widget{
		choice(KeyMaxFPS, label, TextMaxFPS, FPSChoices(f.form.MaxFPS), f.form.MaxFPS, idle,
			func(v int) { s.edit(func(f *Form) { f.MaxFPS = v }) }),
		field(KeyBitrate, TextBitrate, f.bitrate, FieldMaxBitrate, "", func(f *Form, v string) { f.Bitrate = v }),
		choice(KeySound, label, TextSound, SoundChoices(f.form.Sound), f.form.Sound, idle,
			func(v config.Sound) { s.edit(func(f *Form) { f.Sound = v }) }),
		toggle(KeySwap, TextSwap, TextSwapHelp, f.form.SwapCtrlCommand, func(f *Form, v bool) { f.SwapCtrlCommand = v }),
	}
	certificate := m.NewOutlinedButton(ifThen(idle, s.newCertificate), m.NewText(TextNewCertificate), m.OutlinedButtonOpts{Key: KeyNewCertificate})

	// The next time the app opens.
	nextOpen := []m.Widget{
		header(TextNextOpen),
		toggle(KeyStartServer, TextStartServer, "", f.form.StartServer, func(f *Form, v bool) { f.StartServer = v }),
	}

	rows := []m.Widget{m.NewText(TextSettings, m.TextOpts{Style: theme.TextTheme.TitleLarge})}
	if wide {
		group := m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 8}
		pair := func(left, right []m.Widget) m.Widget {
			return m.NewRow([]m.Widget{m.NewExpanded(m.NewColumn(left, group)), m.NewExpanded(m.NewColumn(right, group))},
				m.RowOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 24})
		}
		rows = append(rows, pair(atOnce, nextOpen), header(TextNextSession), pair(listens, sends))
	} else {
		rows = append(rows, atOnce...)
		rows = append(rows, header(TextNextSession))
		rows = append(rows, listens...)
		rows = append(rows, sends...)
		rows = append(rows, certificate)
		rows = append(rows, nextOpen...)
	}
	if f.err != "" {
		rows = append(rows, errorText(bc, f.err, m.WidgetOpts{Key: KeySettingsErr}))
	}
	switch {
	case s.status.Pending:
		rows = append(rows, m.NewText(TextPending, m.TextOpts{Key: KeySettingsNote}))
	case f.note != "":
		rows = append(rows, m.NewText(f.note, m.TextOpts{Key: KeySettingsNote}))
	}
	dirty := f.dirty() && idle
	buttons := []m.Widget{
		m.NewFilledButton(ifThen(dirty, s.saveSettings), m.NewText(TextSaveSettings), m.FilledButtonOpts{Key: KeySaveSettings}),
		m.NewTextButton(ifThen(dirty, s.revertSettings), m.NewText(TextRevert), m.TextButtonOpts{Key: KeyRevert}),
	}
	if wide {
		// The certificate's button at the end of the buttons' row.
		buttons = append(buttons, m.NewSpacer(), certificate)
	}
	rows = append(rows, m.NewRow(buttons, m.RowOpts{Spacing: 8}))
	return card(rows)
}

// flush is the padding of the card's lines: none at the sides, so that
// they start where the card's fields and headings start.
var flush = m.NewEdgeInsetsAll(0)

// ifThen is fn when ok, else nil: a button's callback, nil when it is
// disabled.
func ifThen(ok bool, fn func()) func() {
	if ok {
		return fn
	}
	return nil
}

// choice is a setting chosen from a menu: a line with its label, in
// style, and the menu, which shows value. The label keeps its width; the
// menu has the rest of the line, and cuts a text too long for it.
func choice[T comparable](key m.ValueKey[string], style *m.TextStyle, label string, choices []Choice[T], value T, enabled bool, set func(T)) m.Widget {
	items := make([]*m.DropdownMenuItem[T], len(choices))
	for i, c := range choices {
		items[i] = m.NewDropdownMenuItem(m.NewText(c.Text, m.TextOpts{MaxLines: m.Some(1), Overflow: m.TextOverflowEllipsis}),
			m.DropdownMenuItemOpts[T]{Value: m.Some(c.Value)})
	}
	var on func(m.Opt[T])
	if enabled {
		on = func(v m.Opt[T]) {
			if x, ok := v.Get(); ok {
				set(x)
			}
		}
	}
	return m.NewPadding(m.NewEdgeInsetsSymmetric(4, 0),
		m.NewRow([]m.Widget{
			m.NewText(label, m.TextOpts{Style: style}),
			m.NewExpanded(m.NewDropdownButton(items, on, m.DropdownButtonOpts[T]{Key: key, Value: m.Some(value), IsExpanded: true})),
		}, m.RowOpts{Spacing: 16}))
}
