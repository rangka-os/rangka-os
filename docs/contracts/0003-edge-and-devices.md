# 0003: Edge agent and devices

Status: proposed (1 Nov 2026) · Repo: `rangka-edge` (git@github.com:rangka-os/rangka-edge.git) · Owner: Dede · Licence: ELv2 · Needed when POS starts

## Context
Printers, RFID readers, barcode scanners, cash drawers and scales are plugged in at the outlet, not on the server. They must keep working when the internet is down.

## Decision
1. **Devices are connectors.**
   - Device integrations are ordinary `connector.rk` files in `rangka-connectors` with `via: device`. Examples: `escpos_printer`, `rfid_reader`, `barcode_scanner`, `cash_drawer`.
   - They declare triggers (`rfid.tag_read`, `scanner.scanned`) and actions (`print`, `drawer.open`).
   - Modules decide what a read means (attendance, stock, queue). The connector only reports it.
2. **The kernel runs cloud connectors; Edge runs device connectors.**
   - The kernel sends each registered Edge the device connectors installed on its site.
3. **`rangka-edge` is its own repo** with its own binary (`rangka-edge`, for Windows, macOS and Linux; Android later).
   - It contains generic transports (USB HID, serial, Bluetooth, network printer), a local queue, sync, and device registration with a cashier PIN.
4. **Printing:**
   - a `print:` capability in `system.rk` (`pdf` on the server, `escpos` through Edge)
   - a new kind `print_format.rk` inside modules (receipt, invoice, label)
   - the port `document.print` (see 0002)

## Because Edge is a separate repo
- **Shared code moves to public packages.** Go forbids importing `kernel/internal/...` from another module.
  - `rangka-os` exposes what Edge needs under `kernel/pkg/`: the `.rk` reader, the connector schema types, CEL, and the sync protocol types.
  - Edge imports them at a pinned `rangka-os` tag, like apps do (0000).
- **The sync protocol is versioned.**
  - It is defined in `docs/contracts/0006-edge-sync.md` (to write) as `edge-sync/v1`: register, pull config and connectors, push events, ack.
  - The kernel keeps supporting the previous version for at least one release, so outlets that update late keep working.
- **CI:** `rangka-edge` runs an end-to-end test against the pinned kernel. Steps: register a fake device, take a print job, go offline, queue events, come back online, sync without duplicates.

## Build order (when POS starts)
1. Move the shared packages to `kernel/pkg/`.
2. `edge-sync/v1` in the kernel and in Edge.
3. Network and USB ESC/POS printing.
4. Barcode scanner (keyboard wedge).
5. Cash drawer (through the printer).
6. RFID.
