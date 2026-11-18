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
