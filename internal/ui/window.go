package ui

import (
	"context"
	"log/slog"
	"time"

	"github.com/rm4n0s/glutter/desktop"
	m "github.com/rm4n0s/glutter/material"
	gui "github.com/rm4n0s/glutter/ui"
	"github.com/rm4n0s/mardp/internal/host"
)

// AppID is the app's identifier, the bundle identifier of mardp.app.
const AppID = "com.github.rm4n0s.mardp"

// PollInterval is how often the window asks the backend for the status, so
// that the checklist follows what the user grants in System Settings.
const PollInterval = time.Second

// The keys of the window's buttons, which the tests find them by.
var (
	KeyStart     = m.NewValueKey("start")
	KeyStop      = m.NewValueKey("stop")
	KeySave      = m.NewValueKey("save")
	KeyCancel    = m.NewValueKey("cancel")
	KeyUser      = m.NewValueKey("user")
	KeyPassword  = m.NewValueKey("password")
	KeyActionErr = m.NewValueKey("action error")
	// KeyDisconnect is the connected session's Disconnect button.
	KeyDisconnect = m.NewValueKey("disconnect")
)

// The window's size when it opens (SPEC §12.2): everything it shows fits
// without scrolling.
const (
	WindowWidth  = 1240
	WindowHeight = 800
)

// wideFrom is the width from which the window lays its cards out side by
// side; a narrower one stacks them and scrolls.
const wideFrom = 1000

// EventsShown is how many recent events the window lists, the newest
// first: what fits without scrolling.
const EventsShown = 8

// The keys of the page's scrolling area and of what it holds: the tests
// compare their heights.
var (
	KeyScroll  = m.NewValueKey("page scroll")
	KeyContent = m.NewValueKey("page content")
)

// KeyCheck is the key of the button of the checklist's line titled title.
func KeyCheck(title string) m.ValueKey[string] { return m.NewValueKey("check " + title) }

// Run opens the window on b, with the menu-bar icon, and returns when the
// user chooses Quit or ctx ends; closing the window hides it (SPEC §7.3).
// Glutter logs to log. It must be called on the main goroutine.
func Run(ctx context.Context, b Backend, log *slog.Logger) error {
	return m.RunAppContext(ctx, NewApp(b), m.RunAppOpts{
		AppID:  AppID,
		Window: WindowConfig(b),
		Logger: log,
	})
}

// WindowConfig is the window as it opens: its title bar says the app's
// name and version, and its size shows everything without scrolling
// (SPEC §12.2).
func WindowConfig(b Backend) m.WindowConfig {
	return m.WindowConfig{Title: NewViewModel(b.Status()).Title, Size: m.Size{Width: WindowWidth, Height: WindowHeight}}
}

// App is the Material app around the window, light or dark as the system is.
type App struct {
	m.StatelessWidget
	b Backend
}

// NewApp makes the app of b.
func NewApp(b Backend) *App { return &App{b: b} }

// Build builds the app.
func (a *App) Build(m.BuildContext) m.Widget {
	return m.NewMaterialApp(m.MaterialAppOpts{
		Title:                      NewViewModel(a.b.Status()).Title,
		Theme:                      m.NewThemeData(),
		DarkTheme:                  m.NewThemeDataDark(),
		DebugShowCheckedModeBanner: m.Some(false),
		Home:                       NewWindow(a.b),
	})
}

// Window is the window's page: the status, Start and Stop, and the checklist
// (SPEC §12.2).
type Window struct {
	m.StatefulWidget
	b Backend
}

// NewWindow makes the page of b.
func NewWindow(b Backend, opts ...m.WidgetOpts) *Window {
	return &Window{StatefulWidget: m.NewStatefulWidgetBase(opts...), b: b}
}

// CreateState creates the state.
func (w *Window) CreateState() m.State { return &windowState{} }

// canceler is a timer of the binding.
type canceler interface{ Cancel() }

type windowState struct {
	m.StateOf[*Window]
	status Status
	// busy says that Start, Stop or Save is running.
	busy bool
	// actionErr is why the last action failed.
	actionErr string
	// editing shows the form for the login, and loginErr is why it was
	// not saved.
	editing  bool
	loginErr string
	user     *m.TextEditingController
	password *m.TextEditingController
	timer    canceler
	// settings is the settings card's form.
	settings settingsForm
	// icons are the menu-bar icons of the server's states, and iconErr
	// why they could not be drawn.
	icons   [3]gui.WindowIcon
	iconErr string
	// lifecycle hides the window when it is closed; quitting says that
	// the user chose Quit, which lets the app end.
	lifecycle *m.AppLifecycleListener
	quitting  bool
	// seen is the backend's count of events the window has notified of,
	// and notifyErr why the last notification failed.
	seen      int
	notifyErr string
}

func (s *windowState) InitState() {
	s.user = m.NewTextEditingController()
	s.password = m.NewTextEditingController()
	s.status = s.Widget().b.Status()
	s.seen = s.status.EventTotal
	s.settings.init(s.status.Settings)
	for i, st := range []ServerState{ServerStopped, ServerListening, ServerConnected} {
		icon, err := TrayIcon(st)
		if err != nil {
			s.iconErr = err.Error()
		}
		s.icons[i] = icon
	}
	s.lifecycle = m.NewAppLifecycleListener(m.WidgetsBindingOf(s.Context()), m.AppLifecycleListenerOpts{OnExitRequested: s.exitRequested})
	s.schedule()
}

func (s *windowState) Dispose() {
	if s.timer != nil {
		s.timer.Cancel()
	}
	s.lifecycle.Dispose()
	s.user.Dispose()
	s.password.Dispose()
	s.settings.dispose()
}

// exitRequested answers a request to exit by what asks (SPEC §7.3): the
// window's close button hides the window, and the app goes on with the
// menu-bar icon; a quit (Cmd+Q, the app menu's Quit, the Dock's, the
// menu-bar icon's) and the end of the user's session (a logout, a restart,
// a shutdown) let the app end, which stops the server.
func (s *windowState) exitRequested() gui.AppExitResponse {
	switch m.WidgetsBindingOf(s.Context()).PlatformDispatcher().ExitRequestReason() {
	case gui.AppExitReasonQuit, gui.AppExitReasonSessionEnd:
		return gui.AppExitResponseExit
	}
	if s.quitting {
		return gui.AppExitResponseExit
	}
	if c := s.windowController(); c != nil {
		c.SetHidden(true)
	}
	return gui.AppExitResponseCancel
}

// showWindow brings the window back, hidden or behind others.
func (s *windowState) showWindow() {
	if c := s.windowController(); c != nil {
		c.SetHidden(false)
		c.Activate()
	}
}

// quit ends the app, which stops the server.
func (s *windowState) quit() {
	s.quitting = true
	m.WidgetsBindingOf(s.Context()).ExitApplication(gui.AppExitTypeRequired)
}

// trayAction runs an item of the menu-bar icon's menu.
func (s *windowState) trayAction(a TrayAction) {
	switch a {
	case TrayStart:
		s.start()
	case TrayStop:
		s.stop()
	case TrayDisconnect:
		s.disconnect()
	case TrayShowWindow:
		s.showWindow()
	case TrayQuit:
		s.quit()
	}
}

// schedule asks for the status again after PollInterval. The binding's
// timer runs on the UI thread, by the binding's clock (the tests' fake one).
func (s *windowState) schedule() {
	s.timer = m.WidgetsBindingOf(s.Context()).Gestures().NewTimer(PollInterval, func() {
		s.refresh()
		s.schedule()
	})
}

// refresh shows the backend's status, rebuilding only when it changed.
func (s *windowState) refresh() {
	if st := s.Widget().b.Status(); !st.Equal(s.status) {
		s.SetState(func() {
			s.status = st
			s.settings.follow(st.Settings)
			s.notifyNew(st)
		})
	}
}

// do runs fn on a goroutine with the window busy, and shows its error.
func (s *windowState) do(fn func(ctx context.Context) error, then func(err error)) {
	s.SetState(func() { s.busy, s.actionErr = true, "" })
	s.Go(func(ctx context.Context) {
		err := fn(ctx)
		s.SetState(func() {
			s.busy = false
			if then != nil {
				then(err)
			} else if err != nil {
				s.actionErr = err.Error()
			}
			s.status = s.Widget().b.Status()
			s.notifyNew(s.status)
		})
	})
}

func (s *windowState) start() { s.do(s.Widget().b.Start, nil) }
func (s *windowState) stop()  { s.do(s.Widget().b.Stop, nil) }

func (s *windowState) disconnect() { s.do(s.Widget().b.Disconnect, nil) }

func (s *windowState) openSettings(p host.Permission) {
	s.do(func(ctx context.Context) error { return s.Widget().b.OpenSettings(ctx, p) }, nil)
}

func (s *windowState) editLogin() {
	s.SetState(func() {
		s.editing, s.loginErr = true, ""
		s.user.SetText(s.status.User)
		s.password.SetText("")
	})
}

func (s *windowState) cancelLogin() {
	s.SetState(func() {
		s.editing, s.loginErr = false, ""
		s.password.SetText("")
	})
}

func (s *windowState) saveLogin() {
	user, password := s.user.Text(), s.password.Text()
	s.do(func(ctx context.Context) error { return s.Widget().b.SetLogin(ctx, user, password) }, func(err error) {
		if err != nil {
			s.loginErr = err.Error()
			return
		}
		s.editing, s.loginErr = false, ""
		s.password.SetText("")
	})
}

func (s *windowState) Build(bc m.BuildContext) m.Widget {
	vm := NewViewModel(s.status)
	// No bar at the top: the window's title bar has the name and the
	// version, and the height is the cards'.
	page := m.NewScaffold(m.ScaffoldOpts{
		Body: m.NewLayoutBuilder(func(bc m.BuildContext, c m.BoxConstraints) m.Widget {
			return m.NewSingleChildScrollView(m.NewPadding(m.NewEdgeInsetsAll(pageGap),
				s.cards(bc, vm, c.MaxWidth >= wideFrom), m.PaddingOpts{Key: KeyContent}),
				m.SingleChildScrollViewOpts{Key: KeyScroll})
		}),
	})
	return desktop.NewSystemTray(s.icons[s.status.Server], page, desktop.SystemTrayOpts{
		Title:       TextTrayTitle,
		Tooltip:     TextTrayTitle,
		TooltipBody: vm.State,
		Menu:        trayEntries(NewTrayMenu(vm, s.busy), s.trayAction),
	})
}

// pageGap is the space around the cards and between them.
const pageGap = 10

// cards lays the cards out (SPEC §12.2). A wide window shows everything
// at once: the status, the checklist and the events in one column, the
// settings beside them. A narrow one stacks the cards and scrolls.
func (s *windowState) cards(bc m.BuildContext, vm ViewModel, wide bool) m.Widget {
	// The form of the login: a wide window has it in the settings, in the
	// place of the login's line; a narrow one in the checklist, whose
	// login line comes back for it when the checklist is done.
	left := []m.Widget{s.statusCard(bc, vm, wide)}
	if vm.Checklist != nil || s.editing && !wide {
		left = append(left, s.checklistCard(bc, vm, wide))
	}
	left = append(left, s.eventsCard(bc, vm))
	column := m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStretch, Spacing: pageGap}
	if !wide {
		return m.NewColumn(append(left, s.settingsCard(bc, vm, false)), column)
	}
	return m.NewRow([]m.Widget{
		m.NewExpanded(m.NewColumn(left, column), m.ExpandedOpts{Flex: m.Some(6)}),
		m.NewExpanded(s.settingsCard(bc, vm, true), m.ExpandedOpts{Flex: m.Some(8)}),
	}, m.RowOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: pageGap})
}

// lines are lines of text that belong together, close to each other.
func lines(rows ...m.Widget) m.Widget {
	return m.NewColumn(rows, m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 2})
}

// errorText is text in the theme's color of errors.
func errorText(bc m.BuildContext, text string, opts ...m.WidgetOpts) m.Widget {
	style := m.ThemeOf(bc).TextTheme.BodyMedium.CopyWith(m.TextStyleOpts{Color: m.Some(m.ThemeOf(bc).ColorScheme.Error())})
	o := m.TextOpts{Style: style}
	if len(opts) > 0 {
		o.Key = opts[0].Key
	}
	return m.NewText(text, o)
}

func (s *windowState) statusCard(bc m.BuildContext, vm ViewModel, wide bool) m.Widget {
	theme := m.ThemeOf(bc)
	rows := []m.Widget{m.NewText(vm.State, m.TextOpts{Style: theme.TextTheme.TitleLarge})}
	if len(vm.Addresses) > 0 {
		group := []m.Widget{m.NewText(TextAddresses)}
		for _, a := range vm.Addresses {
			group = append(group, m.NewText(a, m.TextOpts{Style: theme.TextTheme.TitleMedium}))
		}
		rows = append(rows, lines(group...))
	}
	if vm.Fingerprint != "" {
		rows = append(rows, lines(m.NewText(TextFingerprint), m.NewText(vm.Fingerprint, m.TextOpts{Style: theme.TextTheme.BodySmall})))
	}
	if len(vm.Session) > 0 {
		rows = append(rows, lines(sessionRows(vm.Session, wide)...))
	}
	if vm.ViewOnly {
		rows = append(rows, m.NewText(TextViewOnly))
	}
	if vm.Problem != "" {
		rows = append(rows, errorText(bc, TextWhyStopped+vm.Problem))
	}
	if vm.StartProblem != "" {
		rows = append(rows, errorText(bc, TextStartFailed+vm.StartProblem))
	}
	if s.actionErr != "" {
		rows = append(rows, errorText(bc, s.actionErr, m.WidgetOpts{Key: KeyActionErr}))
	}
	if s.iconErr != "" {
		rows = append(rows, errorText(bc, TextIconFailed+s.iconErr))
	}
	var onStart, onStop func()
	if vm.CanStart && !s.busy {
		onStart = s.start
	}
	if vm.CanStop && !s.busy {
		onStop = s.stop
	}
	buttons := []m.Widget{
		m.NewFilledButton(onStart, m.NewText(TextStart), m.FilledButtonOpts{Key: KeyStart}),
		m.NewOutlinedButton(onStop, m.NewText(TextStop), m.OutlinedButtonOpts{Key: KeyStop}),
	}
	if vm.Session != nil {
		buttons = append(buttons, m.NewTextButton(ifThen(!s.busy, s.disconnect), m.NewText(TextDisconnect), m.TextButtonOpts{Key: KeyDisconnect}))
	}
	if s.busy {
		buttons = append(buttons, m.NewCircularProgressIndicator())
	}
	rows = append(rows, m.NewRow(buttons, m.RowOpts{Spacing: 8}))
	return card(rows)
}

// sessionRows are the session's lines as the status card shows them: one
// under the other, or, in a wide window, the short ones two in a row.
func sessionRows(session []string, wide bool) []m.Widget {
	var rows []m.Widget
	for i := 0; i < len(session); i++ {
		if wide && i+1 < len(session) && !sessionLong[i] && !sessionLong[i+1] {
			rows = append(rows, m.NewRow([]m.Widget{m.NewExpanded(m.NewText(session[i])), m.NewExpanded(m.NewText(session[i+1]))},
				m.RowOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 16}))
			i++
			continue
		}
		rows = append(rows, m.NewText(session[i]))
	}
	return rows
}

// eventsCard is the recent events, the newest first.
func (s *windowState) eventsCard(bc m.BuildContext, vm ViewModel) m.Widget {
	rows := []m.Widget{m.NewText(TextEvents, m.TextOpts{Style: m.ThemeOf(bc).TextTheme.TitleLarge})}
	if len(vm.Events) == 0 {
		rows = append(rows, m.NewText(TextNoEvents))
	}
	if len(vm.Events) > 0 {
		group := make([]m.Widget, len(vm.Events))
		for i, e := range vm.Events {
			// One line each, so that the card's height does not depend on
			// the texts; the pointer over a line shows all of it.
			group[i] = m.NewTooltip(m.NewText(e, m.TextOpts{MaxLines: m.Some(1), Overflow: m.TextOverflowEllipsis}),
				m.TooltipOpts{Message: e})
		}
		rows = append(rows, lines(group...))
	}
	if vm.FailedLogins {
		rows = append(rows, m.NewText(WaitRuleText(), m.TextOpts{Style: m.ThemeOf(bc).TextTheme.BodySmall}))
	}
	if s.notifyErr != "" {
		rows = append(rows, errorText(bc, TextNotifyFailed+s.notifyErr))
	}
	return card(rows)
}

// card is a card of rows.
func card(rows []m.Widget) m.Widget {
	return m.NewCard(m.NewPadding(m.NewEdgeInsetsSymmetric(12, 16),
		m.NewColumn(rows, m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 8})),
		m.CardOpts{Margin: m.NewEdgeInsetsAll(0)})
}

func (s *windowState) checklistCard(bc m.BuildContext, vm ViewModel, wide bool) m.Widget {
	theme := m.ThemeOf(bc)
	rows := []m.Widget{m.NewText(TextChecklist, m.TextOpts{Style: theme.TextTheme.TitleLarge})}
	checks := vm.Checklist
	if checks == nil { // all done, and the form is open
		checks = NewViewModel(Status{LoginSet: true, User: s.status.User}).Checklist[2:]
	}
	for _, c := range checks {
		icon := m.NewIcon(m.Icons.RadioButtonUnchecked())
		if c.Done {
			icon = m.NewIcon(m.Icons.CheckCircle(), m.IconOpts{Color: m.Some(theme.ColorScheme.Primary())})
		}
		var button m.Widget
		switch c.Action {
		case ActionOpenSettings:
			p := c.Permission
			var on func()
			if !s.busy {
				on = func() { s.openSettings(p) }
			}
			button = m.NewTextButton(on, m.NewText(c.Button), m.TextButtonOpts{Key: KeyCheck(c.Title)})
		case ActionSetLogin:
			var on func()
			if !s.busy && !s.editing {
				on = s.editLogin
			}
			button = m.NewTextButton(on, m.NewText(c.Button), m.TextButtonOpts{Key: KeyCheck(c.Title)})
		}
		// The button under the text, which so has the card's width.
		text := []m.Widget{
			m.NewText(c.Title, m.TextOpts{Style: theme.TextTheme.TitleMedium}),
			m.NewText(c.Detail, m.TextOpts{Style: theme.TextTheme.BodyMedium.CopyWith(m.TextStyleOpts{Color: m.Some(theme.ColorScheme.OnSurfaceVariant())})}),
		}
		if button != nil {
			text = append(text, button)
		}
		rows = append(rows, m.NewRow([]m.Widget{
			m.NewPadding(m.NewEdgeInsetsFromLTRB(0, 0, 12, 0), icon),
			m.NewExpanded(m.NewColumn(text, m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 2})),
		}, m.RowOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart}))
	}
	if s.editing && !wide {
		rows = append(rows, s.loginForm(bc, false))
	}
	return card(rows)
}

// loginForm is the form of the login; a wide one has its two fields side
// by side.
func (s *windowState) loginForm(bc m.BuildContext, wide bool) m.Widget {
	var onSave, onCancel func()
	if !s.busy {
		onSave, onCancel = s.saveLogin, s.cancelLogin
	}
	password := m.NewInputDecoration(m.InputDecorationOpts{LabelText: m.Some(TextPassword)})
	if s.loginErr != "" {
		password = m.NewInputDecoration(m.InputDecorationOpts{LabelText: m.Some(TextPassword), ErrorText: m.Some(s.loginErr)})
	}
	user := m.NewInputDecoration(m.InputDecorationOpts{LabelText: m.Some(TextUser)})
	fields := []m.Widget{
		m.NewTextField(m.TextFieldOpts{Key: KeyUser, Controller: s.user, Decoration: &user}),
		m.NewTextField(m.TextFieldOpts{Key: KeyPassword, Controller: s.password, Decoration: &password, ObscureText: true}),
	}
	if wide {
		fields = []m.Widget{m.NewRow([]m.Widget{m.NewExpanded(fields[0]), m.NewExpanded(fields[1])},
			m.RowOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 12})}
	}
	return m.NewColumn(append(fields,
		m.NewRow([]m.Widget{
			m.NewFilledButton(onSave, m.NewText(TextSave), m.FilledButtonOpts{Key: KeySave}),
			m.NewTextButton(onCancel, m.NewText(TextCancel), m.TextButtonOpts{Key: KeyCancel}),
		}, m.RowOpts{Spacing: 8}),
	), m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart, Spacing: 8})
}
