# CLAUDE.md

Guidance for Claude Code (claude.ai/code) when working in this repository.

## Overview

A local replacement for the discontinued Plaato cloud service. Plaato Keg scales
speak the Blynk binary protocol over TCP; this server decodes that traffic and
exposes it over REST, WebSocket, a web UI and the BarHelper integration.

This is a Go port of an earlier Elixir implementation.

## Commands

```bash
go build ./...
go test ./...          # everything
go test -race ./...    # what CI runs; keg ingest and HTTP share state
go vet ./...
gofmt -l .             # must print nothing

go run ./cmd/open-plaato-keg        # the server
go run ./cmd/kegsim -addr localhost:4545   # replay recorded hardware traffic at it
```

## Architecture

Raw TCP bytes become stored keg state through three stages:

1. **`internal/blynk`** — the wire protocol. `Framer.Feed` reassembles frames
   from a byte stream; `Frame.Encode` and `ResponseSuccess` build outbound ones.
2. **`internal/plaato`** — interprets frames as Plaato data. `pins.go` maps
   virtual pin numbers to named, typed properties; `decode.go` turns a batch of
   frames into a `Packet`.
3. **`internal/store`** — SQLite persistence. `ApplyPacket` merges a packet into
   the stored keg inside a transaction.

Around that:

- **`internal/keg`** — the TCP listener, the per-connection state machine, the
  registry of live connections, and the commander that writes back to devices.
- **`internal/events`** — an in-process bus. Ingest publishes; the WebSocket hub
  subscribes.
- **`internal/ws`** — broadcasts keg updates to browsers.
- **`internal/api`** — chi router, REST handlers, and the embedded UI.
- **`internal/barhelper`** — forwards volume readings, off the ingest path.
- **`web/static`** — the UI. Plain HTML and JavaScript, no build step, embedded
  into the binary.

## Protocol rules that the hardware depends on

Changing any of these breaks real kegs, and the tests exist to catch that.

- **One acknowledgement per TCP read, not per frame.** The firmware coalesces
  several Blynk messages into a single segment and expects a single 5-byte
  reply.
- **The acknowledgement echoes the first frame's message id.** Older firmware
  validates this before it considers itself connected.
- Outbound message ids are `1..65535`; the device treats 0 as unset.
- `beer_style` and `date` writes are prefixed with a space, matching what the
  Plaato app sends.
- A device is only treated as a keg once it sends a keg-identifying pin. Device
  metadata alone is not enough — a Plaato Airlock sends indistinguishable
  metadata, and accepting it would create phantom kegs.

## Conventions

- Keg ids are the 32-character hex auth token configured on the device.
- Device-reported fields are pointers, so "never reported" stays distinct from
  a genuine zero. An uncalibrated scale really does report 0.
- `beer_left_unit` is always derived from `unit`, `measure_unit` and `keg_mode`,
  never taken from the device's own pin 74.
- Display units are a presentation preference, applied only where the browser
  reads a keg: the two `internal/api` keg handlers and the two `internal/ws`
  send paths, plus the history JSON. Everything else stays in the units the
  device reported — the stored columns, BarHelper, `/get_keg/{deviceID}`, the
  log CSV export and every value on the scale setup page. The converted values
  live in a `display` block that is deliberately absent from `kegColumns`.
- Every WebSocket frame carries a `type`.
- `testdata/capture` is a recording of a real keg session. It is the reference
  for protocol work; regenerate expectations from the current pin map rather
  than trusting older snapshots.
