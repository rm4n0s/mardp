# Manual checks

What only the owner can check, on their own machines. Each item names the step it belongs to. When the owner answers, the answer goes into that step's entry in `docs/IMPLEMENTED.md` and the item is removed; the file is deleted when it is empty.

## 0.4 H.264 in your Remmina (SPEC §19 R5)

On your Linux machine:

1. Run:

   ```bash
   rpm -q openh264 noopenh264 remmina freerdp
   ```

   Expected: `openh264` is installed and `noopenh264` is not. Note the versions of Remmina and FreeRDP.
2. In Remmina, open a new RDP connection profile, Basic tab, "Colour depth". Expected: the list has "GFX AVC420 (32 bpp)".

Tell me the output of step 1 and whether step 2 holds.

## 0.6 Sound with the Mac's output on (SPEC §19 R2)

The probe of M0 plays a 440 Hz tone. Nothing here changes your volume; set it to a quiet level yourself first and unmute. Step 0.9 deleted the probe's source but kept the signed app in `build/m0/Probe.app` (not in git). If that is gone too, rebuild it from the last step that had it: `git worktree add ../mardp-m0 step-0.8 && ../mardp-m0/spikes/probe/build.sh && mkdir -p build/m0 && mv ../mardp-m0/spikes/out/Probe.app build/m0/ && git worktree remove --force ../mardp-m0`.

1. Capture while the tone plays. Expected: you hear the tone for 5 seconds, and the last line says `peak 0.25` (about).

   ```bash
   open -W --stdout /dev/stdout build/m0/Probe.app --args capture -s 5 -w 640 -h 360 -audio -tone
   ```

2. The muting tap. Expected: you hear nothing for 5 seconds, and it prints `peak 0.2500`.

   ```bash
   open -W --stdout /dev/stdout build/m0/Probe.app --args tap -s 5 -tone -mute
   ```

3. The same without muting. Expected: you hear the tone.

   ```bash
   open -W --stdout /dev/stdout build/m0/Probe.app --args tap -s 5 -tone
   ```

Tell me what you heard in each and the printed peaks.

## 0.7 The locked Mac (SPEC §19 R3)

At the Mac (or over Screen Sharing), in the project folder (if `build/m0/Probe.app` is gone, the 0.6 item says how to rebuild it):

1. Run this; it captures for 60 seconds and prints a line every 5 seconds:

   ```bash
   open -W --stdout /dev/stdout build/m0/Probe.app --args capture -s 60 -w 640 -h 360 -report 5 -move-at 40
   ```

2. Within 10 seconds, lock the Mac (Control-Command-Q) and leave it locked for a full minute. At second 40 the probe posts one mouse move of zero distance (the pointer does not move).
3. Unlock, and send me everything it printed.

## 0.7 The recurring Screen Recording question (SPEC §19 R6)

**One is on your screen now** (since before 02:01 on 2026-11-19, during step 0.8): "Probe is requesting to bypass the system private window picker and directly access your screen and audio", with **Allow** and **Open System Settings**. I left it for you. Capture kept working while it stood there. Please:

1. Tell me whether it is still there when you come back (it may have gone away by itself), and which button you pressed.
2. After that, run one short capture and tell me whether the question comes back:

   ```bash
   open -W --stdout /dev/stdout build/m0/Probe.app --args capture -s 5 -w 640 -h 360
   ```

If macOS asks again later whether "mardp probe" (or later "mardp") may record the screen, tell me the date, the exact wording and buttons, and whether a capture or session was running at the time.

## 1.13 Remmina in the test machine's window (SPEC §15.3, §20 Q1)

The test machine's own desktop (Xfce) shows in its UTM window. Nothing here touches your Mac's desktop.

1. Start the machine if it is not running, and open its window in UTM:

   ```bash
   scripts/vm.sh start
   ```

2. On the Mac, in the project folder, serve the test pattern for 10 minutes. It prints the address, user and password to use:

   ```bash
   MARDP_SERVE=10m TMPDIR=$(cd "$TMPDIR" && pwd -P)/ go test -tags mardp_freerdp -run TestServe -v -timeout 15m ./rdp/
   ```

3. In the machine's window, open Remmina and make an RDP profile with that address and user name.
4. Connect with a **wrong** password first. Note what Remmina says, and whether it asks for the password again.
5. Connect with the right password. Expected: eight bars of colour on the top half (white, yellow, cyan, green, magenta, red, blue, dark grey) and colour gradients on the bottom half. Type a few letters, move the mouse and click, then close the connection.
6. When the 10 minutes are over, stop the machine if you started it (`scripts/vm.sh stop`).

Tell me what Remmina said in step 4, whether step 5 looked as expected, and send me the lines the test printed ("session opened", "session ended; input: …").

## 2.5 Keyboard and mouse on the Mac (SPEC §10.4, §10.5)

The tests never post events into your desktop (rule 11), so posting is checked by you, from Remmina, connected to mardp as in 2.7 below (without `-view-only`):

1. Type in TextEdit: letters, digits, punctuation, Shift and Caps Lock, an accented letter through a dead key, and the keypad with Num Lock on and off.
2. Ctrl+arrows (Mission Control's spaces), Alt+letters (Option characters), Super+Tab (the app switcher), Super+C and Super+V (copy and paste).
3. Click, double-click a word, triple-click a line, drag a window, right-click, middle-click a link in Safari, the back and forward buttons in Safari if your mouse has them.
4. Scroll a long page up and down, and sideways if your mouse can: say whether one notch scrolls too little or too much.
5. Hold Shift, close Remmina, and check on the Mac that Shift is not stuck.

Tell me what did not work, and how the scrolling speed felt.

## 2.6 The pointer in Remmina (SPEC §10.3)

Connected from Remmina to mardp as in 2.7 below:

1. Move over text (the I-beam), a link in Safari (the hand), a window's edge (the resize arrows), and start an app (the busy pointer if it shows): the pointer in Remmina changes with each, at the same size as on the Mac, with clean edges.
2. Play a video full screen in Safari and keep the mouse still: the pointer hides.
3. On the Mac itself (or with an app that moves the pointer), move the pointer while Remmina is connected and its mouse is still: Remmina's pointer follows.

Tell me which shapes looked wrong (size, edges, hotspot), and whether 2 and 3 worked.

## 2.7 Remmina shows the Mac's desktop and controls it (SPEC §13)

Runnable once step 2.8 has built `build/mardp.app`, because the user name and the password are set in its window (`mardp -headless` refuses to start without them).

1. Build the app, open it, set a user name and a password, and quit it:

   ```bash
   scripts/build-app.sh
   ```

2. Start the server without a window (macOS ties the permissions to the app, so it is started with `open`):

   ```bash
   open -n build/mardp.app --args -headless -log-file /tmp/mardp.log
   ```

   `/tmp/mardp.log` says `listening` and the certificate's fingerprint; it says `view-only` when Accessibility is not granted.
3. On your Linux machine, in Remmina, connect over RDP to the Mac's address, port 3389, with that user name and password. Expected: the Mac's desktop, which you can control (2.5) with its pointer (2.6).
4. Stop the server:

   ```bash
   pkill -TERM -f mardp.app/Contents/MacOS/mardp
   ```

Tell me whether Remmina showed and controlled the desktop, and what the log said if it did not.
