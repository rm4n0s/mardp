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

## 2.8 The window: the checklist, Start, a connection from Remmina (SPEC §12.2)

With real clicks, at the Mac (not through scripted taps or screenshots of my own). `build/mardp.app` has an identity of its own (`com.github.rm4n0s.mardp`), so macOS treats it as a new app: the permissions you gave the probe do not carry over.

1. Build the app and open it with a log:

   ```bash
   scripts/build-app.sh
   ```

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. Expected on the first start: the status says Stopped; Start is greyed; the checklist "Before the server can start" shows Screen Recording, Accessibility and "User name and password", none done.
3. Press "Open System Settings" on the Screen Recording line: macOS asks its question (the first time) and the Screen Recording pane opens. Turn mardp on there. Expected: within a second or two the line shows a tick and "Granted.", without touching the window. macOS may want mardp to quit and reopen for Screen Recording; if it does, reopen it with the command of step 1. Do the same for Accessibility.
4. Press "Set" on the last line, type a user name and a password of at least 8 characters, and press Save. Expected: the form closes, and once all three lines are done the checklist goes away. (A password of fewer than 8 characters shows why under the field.)
5. Press Start. Expected: the status says Listening, with the addresses to type into Remmina and the certificate's fingerprint. macOS may ask whether mardp may accept incoming connections: allow it.
6. In Remmina on your Linux machine, connect to one of the addresses with that user name and password. Expected: Remmina shows the fingerprint the window shows, then the Mac's desktop; the window says Connected.
7. Press Stop (Remmina disconnects; the status says Stopped), then close the window: the app quits. `pgrep -fl mardp.app` shows nothing.

Tell me what differed at any step, and anything in the window that looked wrong (layout, light or dark mode).


## 3.9 A 1080p video in Remmina (SPEC §9.1, §9.3, §9.5)

Needs Remmina's profile set to H.264: Basic tab, "Colour depth" "GFX AVC420 (32 bpp)". Sound is not there yet (M4).

1. Build the app, open it, and press Start (as in 2.8):

   ```bash
   scripts/build-app.sh
   ```

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. On your Linux machine, connect with Remmina at a 1920×1080 size (or full screen on a 1080p monitor). Expected: the window on the Mac says Connected and "Video: H.264 (AVC420) through the graphics pipeline"; `/tmp/mardp.log` has `msg="video path" … path=avc420` and `msg="H.264 encoder" … fps=60`.
3. On the Mac, play a 1080p video full screen (any video in Safari, or QuickTime). Expected in Remmina: smooth motion at about the video's frame rate, colours like the Mac's (white white, black black, not grey), no smearing or blocks that stay, and the pointer and clicks answering at once.
4. Optional: the same in Remmina in the test machine's window (`scripts/vm.sh start`, then Remmina from its menu, with the Mac's address as the guest sees it: `scripts/vm.sh host-address`). Its decoding is in software on a busy Mac, so slower motion there is expected; only the colours and the absence of errors count.
5. Press Stop and close the window.

Tell me how smooth the video was, whether anything looked wrong, and any line of `/tmp/mardp.log` with `level=WARN` or `level=ERROR`.

## 4.5 Where the sound plays (SPEC §10.1, D17)

mardp mutes your Mac's output only while a client gets the sound, and only if it was not muted already: with your Mac muted, as it is now, it changes nothing. For this check, unmute the Mac and set its volume to a quiet level yourself first; mardp never touches the volume. Have Remmina's profile play the sound locally (Advanced tab, "Audio output mode": "Local").

1. Build the app, open it with a log, check that the setting "Sound" is "On the client only", and press Start:

   ```bash
   scripts/build-app.sh
   ```

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. Play something with sound on the Mac (a video in Safari), then connect with Remmina. Expected: the sound plays on your Linux machine, and the Mac's speakers go quiet; the Mac's sound menu shows it muted; `/tmp/mardp.log` has `msg="the Mac's output is muted while the client plays the sound"`.
3. Disconnect Remmina. Expected: the Mac plays the sound again; the log has `msg="the Mac's output is unmuted"`.
4. Connect again, then end mardp without letting it unmute: `pkill -9 -f mardp.app/Contents/MacOS`. Expected: the Mac stays muted. Open mardp again (step 1's second command). Expected: it unmutes the Mac as it starts; the log has `msg="unmuted the output mardp left muted"`.
5. Set "Sound" to "On both the client and the Mac", press "Save settings", then Stop and Start, and connect. Expected: the sound plays on both machines and the Mac is not muted.
6. Mute the Mac yourself, set "Sound" to "On the client only" again and save, connect and disconnect. Expected: the Mac stays muted all along (the log says `the Mac's output is muted already`).
7. Press Stop and close the window. Put the volume and mute back as you like them.

Tell me what differed at any step.


## 4.6 A video with sound in Remmina, and the clap test (SPEC §10.2, §14)

The test machine proved that FreeRDP plays the sound right, as PCM and as AAC. Only your own Linux machine can show whether it plays without dropouts for minutes and in sync with the picture (SPEC §14: no dropout in 10 minutes, picture and sound at most 100 ms apart). Use any volume you like on the Linux machine; on the Mac, mardp mutes the output during the session only if it is not muted already (4.5), and never touches the volume.

Remmina's profile: Basic tab, "Colour depth" "GFX AVC420 (32 bpp)"; Advanced tab, "Audio output mode" "Local".

1. Build the app, open it with a log, check that the setting "Sound" is "On the client only", and press Start:

   ```bash
   scripts/build-app.sh
   ```

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. Connect with Remmina. Expected: `/tmp/mardp.log` has `msg="sound ready" … format=41222` (AAC; `format=1` is PCM, which is right only if your FreeRDP has no AAC decoder) and `msg="sending sound"`.
3. On the Mac, play a video with sound full screen for 10 minutes (any video in Safari, or QuickTime). Expected on the Linux machine: the sound plays the whole time with no dropouts, clicks or speed changes; the log has no `the sound is behind` line.
4. The clap test: play a video of a clap, a clapperboard or a beep with a flash (search for "audio video sync test" on YouTube; such videos show a flash and a beep together, often with a scale in milliseconds). Watch where the beep falls against the flash. Expected: they seem together; at most 100 ms apart. If you can, film the Linux screen with your phone at 60 or 120 frames a second and count the frames between the flash and the beep.
5. Press Stop and close the window.

Tell me whether the sound dropped out, how far apart the flash and the beep were (and which came first), and any line of `/tmp/mardp.log` with `level=WARN` or `level=ERROR`.

## 5.1 The settings in the real window (SPEC §12.2)

The tests drive the settings card through Glutter's test binding. This check looks at it on the Mac, at its real size, and at a restart for saved settings with Remmina.

1. Build the app and open it with a log:

   ```bash
   scripts/build-app.sh
   ```

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. Scroll to "Settings". Expected: three groups, "These apply at once.", "These apply at the next session: …" and "This applies the next time the app opens.", each with its settings; every field and menu readable, nothing cut off, in light and in dark mode (System Settings, Appearance).
3. Type `70000` as the port and press "Save settings". Expected: "A port from 1 to 65535." under the port, and nothing saved. Press "Undo changes": the port comes back.
4. Type an address that is not your Mac's (for example `10.9.9.9`) and save. Expected: "An IP address of this Mac, or empty for every address." Undo.
5. Open the display menu. Expected: "The main display" and each of your displays with its size.
6. Press Start and connect with Remmina. Set "Frame rate cap" to 30 and save. Expected: "Saved. The changes apply at the next session: …" while Remmina stays connected, at 60 frames a second.
7. Disconnect Remmina. Expected: within a second the pending text goes and `/tmp/mardp.log` has `starting the server again with the saved settings`. Connect again: Remmina connects as before (the log's `the settings are saved` line before it has `maxFps=30`).
8. Press "New certificate" and connect again after disconnecting. Expected: Remmina asks to accept a new certificate, with the fingerprint the window now shows.
9. Set "Frame rate cap" back to 60, save, press Stop and close the window.

Tell me what did not look or behave as expected, with a screenshot of the settings card if something looks wrong.

## 5.2 The session's numbers and the recent events (SPEC §12.2)

The tests check the numbers against the test client. This check compares them with a real Remmina session.

1. Open the app with a log and press Start:

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. Connect with Remmina using a wrong password once, then the right one. Expected: "Recent events" shows the failed login, then the connection, the newest first.
3. Play a video on the Mac. Expected: the status card shows Remmina's computer name and address, the desktop size, "H.264 (AVC420) through the graphics pipeline", "Sound: AAC" (or PCM), about 60 frames a second (30 with the frame rate cap at 30), a bitrate of a few Mbit/s, and a delay of tens of milliseconds. The numbers change about once a second.
4. Press "Disconnect". Expected: Remmina closes saying the server ended the session; the events show "disconnected from this window".
5. Connect again and quit Remmina. Expected: "the client disconnected".

Tell me the numbers you saw, and anything that looked wrong.

## 5.3 The menu-bar icon (SPEC §12.3, §7.3)

The tests drive the icon's menu on Glutter's fake Linux desktop. This check clicks the real icon in the Mac's menu bar.

1. Build the app and open it with a log:

   ```bash
   scripts/build-app.sh
   ```

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. Look at the menu bar. Expected: mardp's icon, a small screen on a stand with an empty screen, drawn in the menu bar's colors (white in dark mode, black in light mode).
3. Click the icon. Expected: a menu with "Stopped" (greyed out), a line, "Start", a line, "Show window" and "Quit".
4. Choose "Start". Expected: the window and the menu say "Listening", the menu has "Stop" in place of "Start", and the icon has a dot in the middle of the screen.
5. Connect with Remmina. Expected: the icon's screen is filled, and the menu has "Connected", "Stop" and "Disconnect". Choose "Disconnect": Remmina closes, and the icon has its dot again.
6. Close the window with its red button. Expected: the window goes, mardp's icon stays, and Remmina can still connect. Choose "Show window": the window comes back as it was. Close it again and click mardp's icon in the Dock: it comes back too.
7. Press Cmd+Q in the window. Expected: the window hides as with the red button (Glutter cannot tell them apart, docs/GLUTTER-ISSUES.md).
8. Choose "Quit" in the icon's menu. Expected: the icon and the app go, and the server stops (Remmina cannot connect).
9. Only when it suits you, since it ends your session: open the app, press Start, and log out of the Mac. Expected: the logout goes on without a message that mardp interrupted it; afterwards `/tmp/mardp.log` ends with "the Mac logs out or shuts down: quitting".

Tell me what did not look or behave as expected, with a screenshot of the menu bar if the icon looks wrong.

## 5.4 Notifications and failed logins (SPEC §12.4, §8.2)

The tests check the notifications on Glutter's fake desktop. This check sees them in macOS's notification center.

1. Open the app and press Start:

   ```bash
   open -n build/mardp.app --args -log-file /tmp/mardp.log
   ```

2. Connect with Remmina. Expected: macOS asks once whether "mardp" may send notifications; allow it. Then a notification "… connected" says from which address and as which user. (If the question came after the connection, connect again.)
3. Click the notification with the window closed. Expected: the window comes back.
4. Disconnect in Remmina. Expected: "… disconnected" with "The client disconnected."
5. In Remmina, try a wrong password five times. Expected: each try is a line in "Recent events"; the fifth says "5 failures, the address waits 1 minute"; the waiting rule shows under the events; one notification only, "Failed logins from <address>", at the fifth. Trying again within the minute is refused at once, with no new line or notification.
6. After the minute, log in with the right password. Expected: it connects.

Tell me what did not look or behave as expected.
