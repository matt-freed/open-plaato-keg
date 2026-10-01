# Architecture

open-plaato-keg is a local replacement for the discontinued Plaato cloud. Plaato
Keg scales speak the Blynk binary protocol over TCP; this server decodes that
traffic, stores each keg's state in SQLite, and exposes it through a REST API, a
WebSocket feed, an embedded web UI and an optional BarHelper integration.

It is a single Go binary with two listeners:

| Listener | Default port | Speaks |
|---|---|---|
| Keg listener | 4545 (`KEG_LISTENER_PORT`) | Blynk over raw TCP, to the scales |
| HTTP server | 8085 (`HTTP_LISTENER_PORT`) | REST, WebSocket (`/ws`) and the UI |

```
                    ┌──────────────────────────── open-plaato-keg ─────────────────────────────┐
                    │                                                                          │
 Plaato Keg ──TCP──►│ keg.Server ─► blynk.Framer ─► plaato.Decode ─► store.ApplyPacket ─► SQLite│
     ▲              │     │  ▲                                              │                   │
     │              │     │  └── Registry ◄── Commander ◄── api (commands)  ├─► events.Bus ─► ws.Hub ──► browsers
     └──── ack /    │     │                                                 │                   │
         commands   │     └─► barhelper.Client (KegAmount) ──► BarHelper    └─► keg_log         │
                    │                                                                          │
                    │ api (chi) ◄──── REST / static UI ────────────────────────────── browsers  │
                    └──────────────────────────────────────────────────────────────────────────┘
```

For a step-by-step trace of one reading from socket to browser, see
[keg-to-websocket.md](keg-to-websocket.md).

## Repository layout

```
cmd/
  open-plaato-keg/   the server: wiring, signal handling, shutdown, history pruning
  kegsim/            replays testdata/capture against a running server
internal/
  blynk/       wire protocol: framing, encoding, command and status codes
  plaato/      Plaato meaning: virtual-pin map, frame batch → Packet
  keg/         TCP listener, per-connection state, registry, commander
  store/       SQLite persistence: kegs, history, taps, beverages, settings
  events/      in-process pub/sub bus
  ws/          WebSocket hub
  api/         chi router, REST handlers, uploads, static UI serving
  barhelper/   rate-limited forwarding of volumes to BarHelper
  units/       unit labels and conversions
  config/      configuration from environment variables
web/
  embed.go     embeds static/ into the binary
  static/      HTML, JavaScript and CSS — no build step
testdata/capture   a recorded real keg session, the reference for protocol work
docs/              this documentation
```

## Startup and shutdown

`cmd/open-plaato-keg/main.go` does all of the wiring in `run()`:

1. **Logging** — a text `slog` handler; level from `LOG_LEVEL`.
2. **Configuration** — `config.Load()`. Invalid values are fatal rather than
   silently defaulted, as is enabling BarHelper without an API key.
3. **Database** — `store.Open()` creates the data directory, opens SQLite in WAL
   mode with a single connection, and applies the embedded `schema.sql`
   (`CREATE … IF NOT EXISTS`, so it is idempotent). Because that leaves an
   existing table untouched, `applySchema` first runs `addMissingColumns`: it
   builds the schema in a scratch in-memory database, compares each existing
   table's columns with it, and adds whatever is missing in one transaction.
   `schema.sql` is therefore the only place a column is declared. This covers
   additions only; a rename, type change, drop or backfill needs a hand-written
   migration, and a column SQLite cannot add (NOT NULL without a default, or a
   primary key) stops startup with an error naming it.
4. **Shutdown context** — `signal.NotifyContext` cancels `ctx` on SIGINT or
   SIGTERM. The Dockerfile uses an exec-form `ENTRYPOINT`, so the binary is PID 1
   and receives `docker stop`'s SIGTERM directly.
5. **Components**, in dependency order: the event bus; the BarHelper client
   (`nil` when disabled, and nil-safe); the keg server and its registry; the
   commander; the WebSocket hub (`go hub.Run`); the daily history pruner.
6. **Listeners** — the keg port is bound synchronously; the HTTP server binds
   inside its own goroutine. Both report fatal errors on a shared channel.

`run()` then blocks until a signal arrives or a listener fails. Shutdown drains
HTTP requests (10 second limit), closes the keg listener, closes every
registered device connection and waits for their goroutines, then — through
deferred calls — stops BarHelper and closes the database.

Background workers (BarHelper, the hub, the pruner) stop by watching
`ctx.Done()`. The two servers do not take the context; they are stopped by
closing their listeners and connections, which unblocks their `Accept` and
`Read` calls.

## The keg pipeline

Raw bytes become stored state through three layers, each ignorant of the one
above it.

### `internal/blynk` — the wire protocol

Every Blynk message is a frame:

```
[cmd uint8][msgID uint16][length uint16][body: length bytes]     big-endian
```

A response frame (cmd 0) is the exception: its third field is a status code and
the frame is exactly 5 bytes.

`Framer.Feed` accepts whatever one socket read produced, appends it to a buffer
and returns every complete frame, keeping any partial frame for the next call.
TCP does not preserve message boundaries — a read may hold several frames, or
part of one — and the Elixir implementation this was ported from failed when a
frame was split across reads. A body length over `MaxBodySize` (8 KiB) is
treated as a corrupt stream and closes the connection.

`ResponseSuccess` builds the acknowledgement and `Frame.Encode` / `NewCommand`
build outbound frames.

### `internal/plaato` — what the frames mean

The Plaato Keg reports everything through Blynk **virtual pins**: numbered
channels whose meaning the firmware defines, not physical GPIO pins. `pins.go`
maps each pin to a property name and type, for example pin 51 →
`amount_left` (float) and pin 49 → `is_pouring` (bool, sent as 255). Pins that
are only ever button echoes, such as tare, are marked `Transient`.

`Decode` turns one batch of frames into a `Packet`:

| Frame | Becomes |
|---|---|
| login / get_shared_dash | `DeviceID`, the 32-hex auth token |
| internal | `Internal`, the metadata map (`fw`, `build`, `h-beat`, …) |
| hardware `vw pin value` | a typed `Property` in `Props` |
| property `pin prop value` | a typed `Property` (e.g. a slider's bounds) |
| unmapped pin | `Unknown`, only when `INCLUDE_UNKNOWN_DATA` is set |
| ping, hardware_sync | nothing |

Values that fail to parse are dropped rather than stored as zero. `Props` keeps
wire order; `Get`, `Float` and `Has` take the last value when a pin repeats.

The device sends one `hardware_sync` read request after logging in, asking for
its configuration pins. The server acknowledges it but does not answer it.

### `internal/keg` — connections

**`Server`** accepts connections and runs `handle` in one goroutine per device,
tracked by a `WaitGroup`. Each loop iteration reads with a 60 second deadline
(the firmware heartbeats every 20 seconds), feeds the framer, sends **one
acknowledgement per read echoing the first frame's message id**, and calls
`ingest`.

**`ingest`** is a small state machine over the per-connection `connState`:

1. A `DeviceID` registers the connection under that keg id.
2. The device is *confirmed* as a keg only once it sends a keg-only pin
   (`amount_left`, `keg_temperature`, `percent_of_beer_left`, `is_pouring`,
   `firmware_version`). A Plaato Airlock sends indistinguishable metadata, and
   accepting that would create phantom kegs.
3. Until confirmed, metadata is buffered and nothing is stored.
4. Once confirmed: `store.ApplyPacket`, a throttled history row, a
   `KegUpdated` event on the bus, and the latest volume to BarHelper.

**`Registry`** maps keg id → live `*Conn` behind an `RWMutex`. It is how
anything outside a device's own goroutine reaches that device.

- `Register` displaces and closes any previous connection for the same id, so a
  keg that reconnects before its old socket times out stays reachable.
- `Unregister` removes an entry only if it still points at the caller's
  connection, so a displaced connection's cleanup cannot remove its
  replacement. When it does remove the entry, `finish` clears the keg's pouring
  flag, since a device that drops mid-pour never reports the pour ending.
- `Conn.Send` serialises writes, because acknowledgements from the read loop
  and commands from HTTP handlers share one socket.

**`Commander`** builds Blynk pin writes — tare, empty-keg weight, max volume,
calibration, units, keg mode, sensitivity, beer style, date — and sends them via
`Registry.Lookup`. Outbound message ids are random in `1..65535`.

### Protocol rules the hardware depends on

- One acknowledgement per TCP read, not per frame.
- The acknowledgement echoes the first frame's message id.
- Outbound message ids are never 0.
- `beer_style` and `date` writes are prefixed with a space.
- A device is only a keg once it sends a keg-identifying pin.

## Persistence — `internal/store`

One SQLite file, opened with WAL journaling, a 5 second busy timeout and
`SetMaxOpenConns(1)`. A single connection serialises all access, so the ingest
path and the API never contend for SQLite's lock.

| Table | Contents |
|---|---|
| `kegs` | One row per keg: device-reported values, app-only values, metadata |
| `keg_log` | History of four readings, keyed by `(keg_id, ts)` |
| `taps` | Tap list entries, optionally linked to a keg and to a display device |
| `beverages` | Beverage library |
| `tap_handles` | Uploaded handle images (files live in the data directory) |
| `app_config` | Key/value settings: theme, display units, home page, time format |

There are no foreign keys.

### The keg row

- **Device-reported columns** are nullable, and pointers in Go, so "never
  reported" stays distinct from a genuine zero — an uncalibrated scale really
  does report 0.
- **App-only columns** (label, beer style, OG/FG/ABV, sort order, display mode)
  are set through the UI and never overwritten by the device.
- **`internal`** and **`extra`** hold metadata and unknown pins as JSON.

`kegColumns` is the single list that drives both the `INSERT` and the `SELECT`,
so the column list, the values and the scan destinations cannot drift apart.

### Applying a packet

`ApplyPacket` → `UpdateKeg` runs a read-modify-write in one transaction: load
the row (or start a new one), apply each non-transient property through
`kegSetters`, merge metadata and unknown pins, update `last_seen`, derive
`beer_left_unit`, then `INSERT OR REPLACE`. Because a packet carries only the
pins that changed, fields it does not mention keep their stored values. The
first confirmed packet from a new keg id creates its row.

Two values get special treatment:

- **`last_pour`** is rejected outside a plausible range (roughly 2 to 48 oz in
  the keg's unit), which filters out spikes such as a fridge compressor
  starting.
- **`beer_left_unit`** is derived from `unit`, `measure_unit` and `keg_mode`
  rather than taken from the device's pin 74, which can go stale after a mode
  change.

### History

After each stored packet, `ingest` writes a `keg_log` row if the keg has at
least one loggable reading and the in-memory `LogThrottle` allows it — at most
one row per keg per minute. The row is a snapshot of the stored keg, not just
the packet. Rows older than 90 days are pruned once a day.

## Live updates — `internal/events` and `internal/ws`

**`events.Bus`** fans events out to subscribers. An event is only a kind
(`KegUpdated` or `KegRemoved`) and a keg id. `Publish` never blocks: a
subscriber that falls behind its 32-event buffer loses events rather than
stalling ingest.

**`ws.Hub`** is the bus's subscriber. It separates receiving events from acting
on them:

- a reader goroutine records each event in a `pending` map keyed by keg id and
  signals a one-slot `wake` channel;
- a flusher goroutine swaps the map out, re-reads each keg from the store,
  applies display units and broadcasts one message per keg.

Every message carries the keg's full current state, so a burst of updates for
one keg costs one database read and one message, and a client that misses a
message is corrected by the next. Each browser connection has a 16-message
buffer; a full buffer drops messages for that client only. New connections are
sent a snapshot of every keg straight away.

Every WebSocket frame carries a `type`: `keg` (with `data`) or `keg_removed`
(with `id`). The design is covered in detail in
[keg-to-websocket.md](keg-to-websocket.md).

## HTTP API and UI — `internal/api`, `web`

A chi router with `Recoverer` and `RealIP` middleware:

| Route | Purpose |
|---|---|
| `GET /api/alive` | Health check (used by the Docker healthcheck) |
| `/api/kegs` | List, connected ids, known ids, ordering |
| `/api/kegs/{id}` | Get, history (`/log`, `/log/csv`), delete |
| `/api/kegs/{id}/…` | Device commands: tare, empty keg, calibration, units, mode, sensitivity, … |
| `/api/taps`, `/api/beverages`, `/api/tap-handles` | CRUD for the tap list; `/api/taps/order` saves a drag-and-drop order through `store.OrderTaps`, which renumbers taps in one transaction |
| `/api/config/…` | Home page, time format, display units, theme |
| `/api/uploads/background` | Background image upload and removal |
| `GET /get_keg/{deviceID}` | Keg data for a tap display device, looked up through `taps.device_id` |
| `GET /ws` | WebSocket feed |
| `/`, `/*` | The embedded UI |

Device commands go through the `Commander`; momentary buttons (tare, empty keg)
are sent as a press and a release. Edits and deletes publish events so open
browsers update.

The UI is plain HTML and JavaScript in `web/static`, embedded into the binary by
`web/embed.go`, with no build step: the dashboard (`index.html`), tap list,
scale setup, history, beverages, tap handles and their setup pages.
`beer-color.js` is the one shared script. A drink's colour is either an SRM or
one of the named presets in `store.ColorPresets` (clear, pink, red, purple,
green, blue) for drinks the SRM scale cannot describe; the API rejects both at
once. The script turns either into a colour for the tap list, which draws
`clear` as a faint tint, and builds the colour picker used by the tap editor and
the beverage library.

### Display units

Stored values always stay in the units the device reported. Conversion to the
user's preferred units is a presentation step, applied only where the browser
reads a keg: the two keg handlers in `internal/api`, the two WebSocket send
paths and the history JSON. The converted values go in a `display` block
alongside the original fields; it is never stored. BarHelper,
`/get_keg/{deviceID}`, the CSV export and the scale setup page all use device
units.

## BarHelper — `internal/barhelper`

Optional forwarding of each mapped keg's remaining volume to BarHelper's custom
keg monitor API, configured by the `BARHELPER_*` variables.

- **Off the ingest path.** `KegAmount` only records the latest volume per
  monitor in memory; a background worker decides what to send.
- **Coalesced, not queued.** Only the newest volume per monitor is kept.
- **Rate limited.** At most one send per monitor per 30 seconds, and at most two
  per 63 seconds across all monitors (BarHelper's limit is per API key; the
  extra three seconds absorb clock skew). The monitor that has waited longest
  goes first.
- **Retried until accepted.** A value is marked sent only on a confirmed
  success, and unchanged values are not resent.

Volumes are sent in the keg's own unit, labelled with `BARHELPER_UNIT`, so that
setting must match how the kegs are configured.

## Concurrency model

| Goroutine | Count | Role |
|---|---|---|
| `main` | 1 | Waits for a signal or a listener error, then shuts down |
| Keg accept loop | 1 | `Server.Serve` |
| Keg connection | one per device | Read, frame, acknowledge, ingest |
| HTTP server | net/http's own | Handlers, including one per WebSocket client |
| WebSocket reader | one per browser | Detects close |
| Hub reader | 1 | Bus → `pending` map |
| Hub flusher | 1 | `pending` → database read → broadcast |
| BarHelper worker | 0 or 1 | Sends due readings every second |
| Pruner | 1 | Daily `keg_log` cleanup |

Shared state is protected where it lives: the registry and hub client set by
`RWMutex`, connection writes by a per-connection mutex, the hub's pending map,
the log throttle and BarHelper state by mutexes, and the database by its single
connection. Cross-goroutine handoffs use buffered channels with non-blocking
sends, so a slow consumer loses data (which the next full-state update
corrects) rather than stalling the keg ingest path. CI runs the tests with
`-race`.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `KEG_LISTENER_PORT` | `4545` | Keg TCP port |
| `HTTP_LISTENER_PORT` | `8085` | HTTP port |
| `DATABASE_FILE_PATH` | `/db/open-plaato-keg.db` | Database; its directory also holds uploads |
| `INCLUDE_UNKNOWN_DATA` | `false` | Store unmapped pins in `extra` |
| `BARHELPER_ENABLED` | `false` | Enable BarHelper forwarding |
| `BARHELPER_API_KEY` | — | Required when enabled |
| `BARHELPER_ENDPOINT` | BarHelper's API | Override the endpoint |
| `BARHELPER_UNIT` | `l` | Unit label sent with each volume |
| `BARHELPER_KEG_MONITOR_MAPPING` | — | `kegToken:monitorId,…` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Testing and tooling

- Unit tests sit beside the code in every package.
- `testdata/capture` is a recording of a real keg session. The capture tests in
  `internal/blynk` and `internal/plaato`, and the journey test, replay it; it is
  the reference for any protocol change.
- `cmd/kegsim` replays the capture against a running server and, like the
  firmware, waits for each acknowledgement, so a broken acknowledgement shows up
  as a timeout. `-token` rewrites the login frames (`setToken`) so the session
  arrives as a different keg; message ids are untouched.
- `testdata/demo.sql` is seed data for a demo database: six kegs on eight taps,
  data only, loaded on top of `internal/store/schema.sql`. Its keg 1 shares the
  capture's token.
- CI (`.github/workflows/ci.yaml`) checks `gofmt`, runs `go vet`,
  `go test -race ./...` and `go build ./...`.
- Releases (`.github/workflows/release.yaml`) run on every push to `main`: the
  tests again, then a multi-arch Docker image (`linux/amd64`, `linux/arm64`)
  pushed to `ghcr.io/matt-freed/open-plaato-keg`.
- `docker-compose.yaml` runs that image with `./data` mounted at `/db`, which
  holds the database and uploads. The container runs as uid 10001, so on Linux
  the directory must be writable by that user.

## Known limitations

- **Shutdown and pouring state.** `Registry.CloseAll` empties the map before
  closing connections, so `finish` cannot unregister them and skips clearing
  the pouring flag. A keg mid-pour at shutdown stays marked as pouring until it
  reports again.
- **Shutdown and unregistered sockets.** `CloseAll` only closes registered
  connections; a socket that has not yet sent an auth token keeps `Shutdown`
  waiting until its 60 second read deadline.
- **Split segments.** If TCP splits a firmware segment so that one read holds a
  complete frame plus part of the next, the server sends two acknowledgements
  for that segment.
- **`hardware_sync` is not answered.** The device's startup request for its
  configuration pins is acknowledged but not replied to.
- **UI reconnection.** The dashboard and scale setup pages do not reconnect
  their WebSocket; the tap list does.
- **Tap order across screens.** A drag-and-drop reorder publishes no event, so
  other open tap lists pick up the new order only at their next minute reload.
- **Kegs without a tap.** The tap list shows taps, so a keg that no tap links to
  appears only on the dashboard.
