// Opens a window of text on this Mac's main display and moves it around,
// then leaves it still, then quits: something that moves on the screen
// for the picture proofs with a real RDP client (SPEC §15.3), without
// posting any input to the desktop.
//
// usage: mover <seconds moving> <seconds still>
import AppKit

let args = CommandLine.arguments
let moving = args.count > 1 ? Double(args[1]) ?? 8 : 8
let still = args.count > 2 ? Double(args[2]) ?? 6 : 6

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let screen = (NSScreen.main ?? NSScreen.screens[0]).visibleFrame
let size = NSSize(width: 640, height: 420)
let window = NSWindow(contentRect: NSRect(origin: NSPoint(x: screen.minX, y: screen.minY), size: size),
    styleMask: [.titled], backing: .buffered, defer: false)
window.title = "mardp motion test: a window that moves"
window.level = .floating
let text = NSTextView(frame: NSRect(origin: .zero, size: size))
text.isEditable = false
text.font = NSFont.monospacedSystemFont(ofSize: 13, weight: .regular)
var lines = ""
for i in 0..<24 {
    lines += String(format: "%2d  the quick brown fox jumps over the lazy dog 0123456789 { x[%d] }\n", i, i * 7)
}
text.string = lines
window.contentView = text
window.orderFrontRegardless()

let start = Date()
Timer.scheduledTimer(withTimeInterval: 1.0 / 60, repeats: true) { _ in
    let e = Date().timeIntervalSince(start)
    if e < moving {
        let x = screen.minX + (screen.width - size.width) * (0.5 - 0.5 * cos(e * 1.7))
        let y = screen.minY + (screen.height - size.height) * (0.5 - 0.5 * cos(e * 1.1))
        window.setFrameOrigin(NSPoint(x: x, y: y))
    } else if e > moving + still {
        app.terminate(nil)
    }
}
app.run()
