package host

import (
	"context"
	"image"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/rm4n0s/mardp/rdp"
)

// Stand-ins for the Mac's parts, for the tests and for a host without the
// Mac's (SPEC §4.1).

// The moving square of the test pattern.
const (
	squareSide = 64
	squareStep = 8
)

// PatternScreen is a screen source that shows a test pattern: color bars
// over gradients, and a white square that moves across the middle by a few
// pixels a frame.
type PatternScreen struct {
	// Info is the display it says it captures; its zero value is a
	// 1920×1080 display of the same size in points.
	Info Display
	// Frames is how many frames it sends before it stops changing; 0 means
	// no end.
	Frames int
}

// display is the display s says it captures.
func (s *PatternScreen) display() Display {
	if s.Info.Width > 0 && s.Info.Height > 0 {
		return s.Info
	}
	return Display{ID: "pattern", Name: "Test pattern", Width: 1920, Height: 1080, PointWidth: 1920, PointHeight: 1080, Main: true}
}

// Display implements ScreenSource.
func (s *PatternScreen) Display(ctx context.Context) (Display, error) { return s.display(), nil }

// Capture implements ScreenSource: frame n of the pattern MaxFPS times a
// second, each with the two rectangles that changed.
func (s *PatternScreen) Capture(ctx context.Context, cfg ScreenConfig, frame func(*Frame)) error {
	img := &rdp.Image{Pix: make([]byte, 4*cfg.Width*cfg.Height), Stride: 4 * cfg.Width, Width: cfg.Width, Height: cfg.Height}
	PatternFrame(img, 0)
	f := Frame{Image: img}
	frame(&f)
	var dirty [2]image.Rectangle
	f.Dirty = dirty[:]
	t := time.NewTicker(time.Second / time.Duration(max(cfg.MaxFPS, 1)))
	defer t.Stop()
	for n := 1; s.Frames == 0 || n < s.Frames; n++ {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		dirty[0], dirty[1] = patternSquare(cfg.Width, cfg.Height, n-1), patternSquare(cfg.Width, cfg.Height, n)
		paint(img, dirty[0], patternBackground)
		paint(img, dirty[1], patternWhite)
		frame(&f)
	}
	<-ctx.Done()
	return nil
}

// PatternFrame draws frame n of PatternScreen's pattern into img.
func PatternFrame(img *rdp.Image, n int) {
	paint(img, image.Rect(0, 0, img.Width, img.Height), patternBackground)
	paint(img, patternSquare(img.Width, img.Height, n), patternWhite)
}

// patternSquare is where the square is in frame n of a w×h pattern.
func patternSquare(w, h, n int) image.Rectangle {
	side := min(squareSide, w, h)
	x := n * squareStep % max(w-side+1, 1)
	y := (h - side) / 2
	return image.Rect(x, y, x+side, y+side)
}

// paint sets each pixel of r in img to color(x, y, width, height), as B, G,
// R, A.
func paint(img *rdp.Image, r image.Rectangle, color func(x, y, w, h int) (b, g, rr byte)) {
	r = r.Intersect(image.Rect(0, 0, img.Width, img.Height))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[y*img.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			b, g, rr := color(x, y, img.Width, img.Height)
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = b, g, rr, 0xff
		}
	}
}

// patternBars are the colors of the bars, as B, G, R.
var patternBars = [...][3]byte{
	{0xff, 0xff, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0x00}, {0x00, 0xff, 0x00},
	{0xff, 0x00, 0xff}, {0x00, 0x00, 0xff}, {0xff, 0x00, 0x00}, {0x00, 0x00, 0x00},
}

// patternBackground is the pattern without the square: eight bars in the
// top half, a red and a blue gradient in the bottom half.
func patternBackground(x, y, w, h int) (b, g, r byte) {
	if y < h/2 {
		c := patternBars[x*len(patternBars)/w]
		return c[0], c[1], c[2]
	}
	v := byte(x * 255 / max(w-1, 1))
	if y < 3*h/4 {
		return 0, 0, v
	}
	return v, 0, 0
}

// patternWhite is the square's color.
func patternWhite(x, y, w, h int) (b, g, r byte) { return 0xff, 0xff, 0xff }

// ToneSource is a sound source that plays a sine tone on both channels, in
// buffers of 20 ms.
type ToneSource struct {
	// Frequency is the tone's pitch in hertz; 0 means 440.
	Frequency float64
}

// toneBuffer is how long each buffer of ToneSource is.
const toneBuffer = 20 * time.Millisecond

// Capture implements SoundSource.
func (s *ToneSource) Capture(ctx context.Context, buf func(*SoundBuffer)) error {
	freq := s.Frequency
	if freq <= 0 {
		freq = 440
	}
	n := SoundRate * int(toneBuffer) / int(time.Second)
	b := SoundBuffer{Left: make([]float32, n), Right: make([]float32, n)}
	t := time.NewTicker(toneBuffer)
	defer t.Stop()
	for i := 0; ; i++ {
		for j := range n {
			v := float32(0.25 * math.Sin(2*math.Pi*freq*float64(i*n+j)/SoundRate))
			b.Left[j], b.Right[j] = v, v
		}
		b.Time = time.Duration(i) * toneBuffer
		buf(&b)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// InputKind is the kind of an InputEvent.
type InputKind int

// The kinds of InputEvent, one per method of Injector.
const (
	InputKey InputKind = iota + 1
	InputUnicode
	InputMove
	InputButton
	InputWheel
	InputLocks
)

// InputEvent is one call RecordingInjector got; the fields of its kind are
// set.
type InputEvent struct {
	Kind InputKind
	// Key: Scancode, Extended, Down. Unicode: Rune, Down.
	Scancode uint8
	Extended rdp.Extended
	Rune     rune
	Down     bool
	// Move and Button: X, Y. Button: Button, Down, Clicks.
	X, Y   float64
	Button rdp.MouseButton
	Clicks int
	// Wheel: DX, DY.
	DX, DY int
	// Locks: Caps, Num, Scroll.
	Caps, Num, Scroll bool
}

// RecordingInjector is an injector that records what it is given.
type RecordingInjector struct {
	// Err, when set, is what every call returns, after it is recorded.
	Err error

	mu      sync.Mutex
	events  []InputEvent
	changed chan struct{}
}

// record adds e.
func (in *RecordingInjector) record(e InputEvent) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.events = append(in.events, e)
	if in.changed != nil {
		close(in.changed)
		in.changed = nil
	}
	return in.Err
}

// Events returns what was recorded so far.
func (in *RecordingInjector) Events() []InputEvent {
	in.mu.Lock()
	defer in.mu.Unlock()
	return slices.Clone(in.events)
}

// Wait waits until n events are recorded and returns them all, or returns
// those recorded when ctx ends first.
func (in *RecordingInjector) Wait(ctx context.Context, n int) []InputEvent {
	for {
		in.mu.Lock()
		if len(in.events) >= n {
			defer in.mu.Unlock()
			return slices.Clone(in.events)
		}
		if in.changed == nil {
			in.changed = make(chan struct{})
		}
		ch := in.changed
		in.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return in.Events()
		}
	}
}

// Key implements Injector.
func (in *RecordingInjector) Key(scancode uint8, extended rdp.Extended, down bool) error {
	return in.record(InputEvent{Kind: InputKey, Scancode: scancode, Extended: extended, Down: down})
}

// Unicode implements Injector.
func (in *RecordingInjector) Unicode(r rune, down bool) error {
	return in.record(InputEvent{Kind: InputUnicode, Rune: r, Down: down})
}

// MouseMove implements Injector.
func (in *RecordingInjector) MouseMove(x, y float64) error {
	return in.record(InputEvent{Kind: InputMove, X: x, Y: y})
}

// MouseButton implements Injector.
func (in *RecordingInjector) MouseButton(button rdp.MouseButton, down bool, x, y float64, clicks int) error {
	return in.record(InputEvent{Kind: InputButton, Button: button, Down: down, X: x, Y: y, Clicks: clicks})
}

// Wheel implements Injector.
func (in *RecordingInjector) Wheel(dx, dy int) error {
	return in.record(InputEvent{Kind: InputWheel, DX: dx, DY: dy})
}

// SetLocks implements Injector.
func (in *RecordingInjector) SetLocks(caps, num, scroll bool) error {
	return in.record(InputEvent{Kind: InputLocks, Caps: caps, Num: num, Scroll: scroll})
}

// StaticPointer is a pointer source whose pointer can be set.
type StaticPointer struct {
	mu    sync.Mutex
	shape PointerShape
	err   error
	reads int
}

// Set sets the pointer, or the error reading it returns.
func (p *StaticPointer) Set(shape PointerShape, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shape, p.err = shape, err
}

// Reads says how many times the pointer was read.
func (p *StaticPointer) Reads() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads
}

// Current implements PointerSource.
func (p *StaticPointer) Current() (PointerShape, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	return p.shape, p.err
}

// FakeSystem is a System whose answers are set.
type FakeSystem struct {
	mu       sync.Mutex
	granted  map[Permission]bool
	displays []Display
	requests []Permission
	opened   []Permission
	awake    int
}

// NewFakeSystem makes a FakeSystem of the displays, with the permissions
// granted.
func NewFakeSystem(displays []Display, granted ...Permission) *FakeSystem {
	s := &FakeSystem{granted: map[Permission]bool{}, displays: displays}
	for _, p := range granted {
		s.granted[p] = true
	}
	return s
}

// Grant grants or withdraws p.
func (s *FakeSystem) Grant(p Permission, granted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.granted[p] = granted
}

// Granted implements System.
func (s *FakeSystem) Granted(p Permission) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.granted[p]
}

// Request implements System: it records the request.
func (s *FakeSystem) Request(p Permission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, p)
	return nil
}

// Requests returns the permissions requested so far.
func (s *FakeSystem) Requests() []Permission {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// OpenSettings implements System: it records the pane opened.
func (s *FakeSystem) OpenSettings(ctx context.Context, p Permission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened = append(s.opened, p)
	return nil
}

// Opened returns the panes of System Settings opened so far.
func (s *FakeSystem) Opened() []Permission {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.opened)
}

// Displays implements System.
func (s *FakeSystem) Displays() ([]Display, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.displays), nil
}

// KeepAwake implements System: Awake counts the holds not released.
func (s *FakeSystem) KeepAwake() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.awake++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.awake--
		})
	}, nil
}

// Awake says how many holds of KeepAwake are not released.
func (s *FakeSystem) Awake() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.awake
}
