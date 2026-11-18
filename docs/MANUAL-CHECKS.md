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

The probe of M0 plays a 440 Hz tone. Nothing here changes your volume; set it to a quiet level yourself first and unmute. If `spikes/` is already gone (step 0.9 deletes it), first run `git worktree add ../mardp-0.6 step-0.6 && cd ../mardp-0.6 && spikes/probe/build.sh`, and afterwards `cd - && git worktree remove ../mardp-0.6`.

1. Capture while the tone plays. Expected: you hear the tone for 5 seconds, and the last line says `peak 0.25` (about).

   ```bash
   open -W --stdout /dev/stdout spikes/out/Probe.app --args capture -s 5 -w 640 -h 360 -audio -tone
   ```

2. The muting tap. Expected: you hear nothing for 5 seconds, and it prints `peak 0.2500`.

   ```bash
   open -W --stdout /dev/stdout spikes/out/Probe.app --args tap -s 5 -tone -mute
   ```

3. The same without muting. Expected: you hear the tone.

   ```bash
   open -W --stdout /dev/stdout spikes/out/Probe.app --args tap -s 5 -tone
   ```

Tell me what you heard in each and the printed peaks.
