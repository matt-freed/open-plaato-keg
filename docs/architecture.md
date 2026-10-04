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
  store/       SQLite persistence: kegs, history, taps, settings
  events/      in-process pub/sub bus
  ws/          WebSocket hub
  api/         chi router, REST handlers, static UI serving
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
   additions only: a table or column removed from `schema.sql`  stays
   in older databases, which is harmless because every query names its
   columns. A rename, type change or backfill needs a hand-written migration,
   and a column SQLite cannot add (NOT NULL without a default, or a primary
   key) stops startup with an error naming it.
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
4. Once confirmed: `store.ApplyPacket` (which also records a pour when the
   packet ends one), a throttled history row, a `KegUpdated` event on the bus,
   and the latest volume to BarHelper.

**`Registry`** maps keg id → live `*Conn` behind an `RWMutex`. It is how
anything outside a device's own goroutine reaches that device.

- `Register` displaces and closes any previous connection for the same id, so a
  keg that reconnects before its old socket times out stays reachable.
- `Unregister` removes an entry only if it still points at the caller's
  connection, so a displaced connection's cleanup cannot remove its
  replacement. When it does remove the entry, `finish` clears the keg's pouring
  flag with `SetPouring`, since a device that drops mid-pour never reports the
  pour ending. That also ends and records the pour in progress.
- `Conn.Send` serialises writes, because acknowledgements from the read loop
  and commands from HTTP handlers share one socket.

**`Commander`** builds Blynk pin writes — tare, empty-keg weight, max volume,
calibration, units, keg mode, sensitivity — and sends them via
`Registry.Lookup`. Outbound message ids are random in `1..65535`.

### Protocol rules the hardware depends on

- One acknowledgement per TCP read, not per frame.
- The acknowledgement echoes the first frame's message id.
- Outbound message ids are never 0.
- A device is only a keg once it sends a keg-identifying pin.

## Persistence — `internal/store`

One SQLite file, opened with WAL journaling, a 5 second busy timeout and
`SetMaxOpenConns(1)`. A single connection serialises all access, so the ingest
path and the API never contend for SQLite's lock.

| Table | Contents |
|---|---|
| `kegs` | One row per keg: device-reported values, app-only values, metadata |
| `keg_log` | History of four readings, keyed by `(keg_id, ts)` |
| `pours` | One row per detected pour, with a copy of the tap's beer at the time; never pruned |
| `taps` | Tap list entries, optionally linked to a keg (at most one tap per keg, checked by `SaveTap`) and to a display device |
| `app_config` | Key/value settings: theme, display units, amount display, home page, time format, minimum pour |

There are no foreign keys.

### The keg row

- **Device-reported columns** are nullable, and pointers in Go, so "never
  reported" stays distinct from a genuine zero — an uncalibrated scale really
  does report 0.
- **App-only columns** (label, sort order, CO2 capacity)
  are set through the UI and never overwritten by the device.
- **`internal`** and **`extra`** hold metadata and unknown pins as JSON.
- **Timestamps.** `first_seen` is set when the row is created. `last_seen` is
  when the device last sent a data packet, stamped only by `ApplyPacket`.
  `barhelper_last_sent` is when BarHelper last accepted a reading, written by
  `RecordBarHelperSent`; 0 means never.
- **`pour_started_at`** and **`pour_start_amount`** hold the pour in progress
  (see Pours below). They are server state, so they are stored but tagged
  `json:"-"`.

`kegColumns` is the single list that drives both the `INSERT` and the `SELECT`,
so the column list, the values and the scan destinations cannot drift apart.

### Applying a packet

`ApplyPacket` → `updateKegTx` runs a read-modify-write in one transaction: load
the row (or start a new one), apply each non-transient property through
`kegSetters`, merge metadata and unknown pins, stamp `last_seen`, run
`trackPour`, derive `beer_left_unit`, then `INSERT OR REPLACE`. `UpdateKeg` is
the same without access to the transaction. Because a packet carries only the
pins that changed, fields it does not mention keep their stored values. The
first confirmed packet from a new keg id creates its row.

Two values get special treatment:

- **`last_pour`**, the device's own pin 59, is rejected outside a plausible
  range (`pourRange`, roughly 2 to 48 oz in the keg's unit), which filters out
  spikes such as a fridge compressor starting. It is kept and served, but
  nothing in the UI shows it any more: the Kegs page's last pour comes from
  recorded pours.
- **`beer_left_unit`** is derived from `unit`, `measure_unit` and `keg_mode`
  rather than taken from the device's pin 74, which can go stale after a mode
  change.

### History

After each stored packet, `ingest` writes a `keg_log` row if the keg has at
least one loggable reading and the in-memory `LogThrottle` allows it — at most
one row per keg per minute. The row is a snapshot of the stored keg, not just
the packet.

Two jobs keep the table bounded, run by `prune` in `main.go` at startup and
once a day after that:

- `PruneLog` deletes rows older than the retention (`LOG_RETENTION_DAYS`, 365
  by default; 0 turns it off).
- `CompactLog` replaces rows older than `LOG_COMPACT_AFTER_DAYS` (30 by
  default; 0 turns it off) with one row per keg per hour (`CompactStep`),
  timestamped at the start of the hour. Amount, percent and temperature are
  averaged, a reading nobody reported stays NULL, and the hour is pouring if
  any reading in it was. It works one keg-day per transaction so the first pass
  over a long history never holds the write lock for long, and it skips hours
  already reduced to one aligned row, so a second run changes nothing. Pours
  are stored separately and are unaffected.

Reads for the chart are averaged too. `logRanges` in `internal/api/history.go`
gives each range a step: none up to 24 hours, then 10 minutes for 7 days, 30
minutes for 30 days, and 2, 3 and 8 hours for 90 days, 180 days and a year,
so no chart exceeds 1,440 points. Past 30 days each step is a whole number of
compacted hours. `ReadLogSampled` groups by `ts / step`, timestamps each point
at the average time of its readings, and averages them the same way as
`CompactLog`. `handleKegLog` reports the step in `X-Log-Step-Seconds`. The CSV
export is never averaged; it is every row stored for the range.

### Pours

A pour is one pouring window reported by the keg. `trackPour` in
`internal/store/pour.go` runs inside `ApplyPacket` and `SetPouring`, with the
keg's pouring flag and amount from before the change:

- **Start.** `is_pouring` turns on. The start time and the amount left from
  *before* the packet go in `pour_started_at` and `pour_start_amount`, so a pour
  that starts in the same packet as the first falling reading is measured from
  the right place, and a restart mid-pour does not lose it.
- **End.** `is_pouring` turns off, or the keg disconnects and `finish` calls
  `SetPouring(false)`. The size is the start amount minus the amount left after
  the packet, in the keg's `beer_left_unit`. The firmware sends its settled
  amount before it clears pin 49.
- **Recorded** only if the keg is in beer mode, both amounts are known, and the
  size is at least the minimum pour and at most `maxPourGal` (128 US fl oz,
  converted into the keg's unit). The cap catches a lifted keg or a vibration
  spike while still allowing a pitcher. Anything else is logged at debug level
  and dropped.

Scale jitter outside a pouring window can never become a pour. The minimum
pour (`MinPour`, default 2 oz, entered in oz or ml on Dashboard Setup) is read
by `minPourTx` when a pour ends and converted into the keg's unit by
`MinPour.In`. It is applied once, so changing it only affects future pours.

`insertPour` copies the beer's name, style, ABV and tap number from the first
tap by tap number that draws from the keg, plus the keg's label, so editing the
tap or kegging a new beer later does not rewrite past pours. Each pour also
records its own unit, unlike `keg_log`.

Pours are never pruned. `ClearLog` and `DeleteKeg` set `hidden_from_keg` rather
than deleting them: they drop out of that keg's history (`ListKegPours`) but
stay in the list of all pours (`ListPours`). `DeletePour` removes one pour
everywhere.

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
| `/api/kegs/{id}` | Get, history (`/log`, `/log/csv`, `/log/clear`), pours (`/pours`), delete |
| `/api/kegs/{id}/…` | Device commands: tare, empty keg, calibration, units, mode, sensitivity, … |
| `/api/taps` | CRUD for the tap list; saving a tap with a keg another tap uses is a 409, from `store.KegInUseError`; `/api/taps/order` saves a drag-and-drop order through `store.OrderTaps`, which renumbers taps in one transaction |
| `/api/pours` | Every pour from every keg (`?range=` 24h, 7d, 30d, 90d, 1y or all), `/api/pours/csv` in device units, `/api/pours/{id}/delete` |
| `/api/config/…` | Home page, time format, display units, amount display, minimum pour, theme |
| `GET /ws` | WebSocket feed |
| `/`, `/*` | The embedded UI |

Device commands go through the `Commander`; momentary buttons (tare, empty keg)
are sent as a press and a release. Edits and deletes publish events so open
browsers update.

The UI is plain HTML and JavaScript in `web/static`, embedded into the binary by
`web/embed.go`, with no build step: the Kegs page (`kegs.html`), tap list,
Keg Setup, history, All Pours and their setup pages. A tap holds all of its drink's
details; there is no separate beverage library.
Every page shares one header bar, the `<site-header>` custom element in
`site-header.js` with its styles in `site-header.css`. It renders the page
title from its `heading` attribute, the Tap List, Kegs and History links, the
Configure menu with the server version, and marks the current page. All Pours
has no link of its own; it is reached from History, which stays marked there. Pages load
it in `<head>` without `defer`, so the element is defined before the parser
reaches it. The tap list sets the beer count beside its title through the
element's `count` property.

The pages are styled as one application, in the tap list's look:

- `tokens.css` holds the design tokens every page loads first: the page, tile
  and text colours, the greys for secondary text, the radii, the fonts and
  `--action`, the accent used for main buttons and selections. The themeable
  ones read the variables from `/theme.css` (see Theme below).
- `tiles.css` is the tile grid and tile shared by the tap list and the Kegs
  page: the heading, specs, readings and the keg graphic.
- `keg-graphic.js` draws that graphic, `kegSvg()`, and `kegLevelTransform(pct)`
  sets its level. The tap list, the Kegs page and the Dashboard Setup preview
  use it.
- `style.css` styles everything else, used by every page except the tap list:
  layout, tiles for groups of settings, form controls, segmented choices,
  tables and toasts.

Tap Setup and Keg Setup share one pattern: a list of taps or scales in a
single centred column, where choosing one opens its editor as a view of its
own with a back link to the list. The URL hash records the open item
(`#tap=<id>`, `#new`, `#keg=<id>`), and each page's `showView` follows it, so
Back and reload work. Keg Setup lists every known scale with whether it is
connected, polling `/api/kegs/connected` since connections publish no event.

The History page (`history.html`) charts one scale's `/api/kegs/{id}/log` for
the chosen range. Amount and temperature are two charts sharing a time axis
rather than one chart with two y-axes, and a line breaks where readings stop
for more than ten minutes, or three steps on an averaged range. Ranges run from
1 hour to 1 year. `AXIS_TICKS` places the time axis's ticks on clock or calendar
boundaries: at most seven for most ranges, so Chart.js never thins them out,
and one per month over a year.
`axisLabel` then puts the date under the first tick and wherever the day turns
on ranges of a day or less, and the year under the first tick and wherever the
year turns on longer ones. The hover readout and the Pours table add the year
to any date outside the current one. The amount chart's Fit/Full toggle (`axisScale`) picks its
y-axis: Fit, the default, pads the range's lowest and highest value by 15%
in `fitAxis`, so a pour from a nearly full keg is a visible step; Full starts the axis at zero.
Its poured and pours figures, the pour markers on the amount chart and the
Pours table below the charts all come from the stored pours in
`/api/kegs/{id}/pours`; a marker sits at the pour's end time, on the nearest
logged reading. "Left now" is the keg's current reading, fetched with each
range, rather than the last point, which on a long range is an average. Each
row of the table can be deleted.
Clear history posts to `/api/kegs/{id}/log/clear`, which `handleClearKegLog`
serves with `store.ClearLog`: every reading for that keg goes, in every range,
and its pours leave the page, while the keg and the other kegs' history stay.
The pours remain on All Pours. The page asks for confirmation first, since the
history is not recoverable.

The All Pours page (`pours.html`), linked from History, lists `/api/pours` for
every keg, including pours hidden from a cleared or deleted scale's history,
which are tagged. It reaches further back than History (90 days, a year, all
time), since pours are never pruned. Beer and scale filters run in the browser
over the loaded range; the URL hash records range and filters. A summary gives
the count, the total poured (summed per unit, as scales can be in oz or ml)
and a per-beer breakdown. Download CSV fetches `/api/pours/csv` in device
units.

The Kegs page (`kegs.html`) draws a tile per scale with the scale's label as
its heading. A second specs line under the status line shows the keg's
`latest_pour`, the newest pour still in its history: its size, then its time
(`pourWhen`: the time of day, with the date in front when it was not today, in
the chosen clock format). A scale with no such pour, such as one whose history was just
cleared, shows none. It fetches `/api/taps` to show the beer on the tap a scale feeds
and to fill the keg in that beer's colour. The specs row under each
tile's name shows how long ago the device last sent data (`last_seen`) and how
long ago BarHelper last accepted a reading (`barhelper_last_sent`, left out when
0). The Wi-Fi strength sits at the end of the top row while the scale is
connected; the page re-renders every 15 seconds to keep those times
current. Badges beside "Pouring" in the top row mark a scale reporting a leak
(`leak_detection` is 1), which also reddens the tile's border, and a scale
with no live connection (`connected` is false), whose readings and graphic are
dimmed. A disconnect publishes a keg update, so the offline badge appears as
soon as the server notices: at once for a clean close, and within
`ReadTimeout` (60 seconds) for a scale that drops off the network.

`beer-color.js` is shared by the tap list, the Kegs page and the tap editor. A drink's colour is either an SRM or
one of the named presets in `store.ColorPresets` (clear, pink, red, purple,
green, blue) for drinks the SRM scale cannot describe; the API rejects both at
once. The script turns either into a colour for the tap list, which draws
`clear` as a faint tint, and builds the colour picker used by the tap editor.

### Theme

Dashboard Setup stores the theme through `store.SetTheme`, and
`handleThemeCSS` serves it as `/theme.css`, a set of custom properties
(`--bg-color`, `--card-bg`, `--text-color`, `--font-family`, the accent and the
two tap list fonts). Every page links it, and `tokens.css` reads every one of
them, so they reach all pages. The `var()` fallbacks in `tokens.css` hold the
defaults, which are the tap list's palette, the system font and an amber
accent, so an unset value leaves the page in its default look. The tap list
does not use the accent, since each tile takes its beer's colour. The tap list
title font is also the font of the page title in the header bar.
`fontStack` turns a stored family name into a full stack that falls back to the
system font, and `System` selects that stack with no Google Fonts import.
`GetAppConfig` runs a stored theme through `dropLegacyThemeDefaults`, which
clears the settings page's former defaults. Every save used to post them while
the colours had no effect, so they are treated as unset.

### Display units

Stored values always stay in the units the device reported. Conversion to the
user's preferred units is a presentation step, applied only where the browser
reads a keg: the two keg handlers in `internal/api`, the two WebSocket send
paths, the history JSON and the two pours JSON endpoints. The converted values
go in a `display` block alongside the original fields; it is never stored. The
same keg boundaries also add `latest_pour` with `store.SetLatestPours`, which
is not a column either.
`ConvertPours` converts each pour from its own stored unit and always scales it
to a pour-sized sub-unit (oz, ml or g), even when following the device.
BarHelper, the CSV exports and the Keg Setup page all use device units.

### Amount display

One app-wide setting, `amount_display` (`store.SetAmountDisplay`), chooses
whether every keg graphic shows the amount left or the percentage left as its
large figure, on both the tap list and the Kegs page; the Kegs page shows the
other figure among the tile's readings. CO₂ cylinders always show the amount.
It is a presentation choice only and is not sent to the device. Both pages
read it from `/api/config/amount-display` when they load and on their minute
reload, so a change reaches an open screen within a minute.

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
- **Recorded.** Each accepted send is passed to the client's `SentRecorder`,
  which is the store: `RecordBarHelperSent` sets the keg's `barhelper_last_sent`
  with a plain `UPDATE`, so a send that lands after the keg was forgotten does
  not bring its row back.

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
| Pruner | 1 | Daily `keg_log` pruning and hourly compaction |

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
| `DATABASE_FILE_PATH` | `/db/open-plaato-keg.db` | Database |
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
- `testdata/demo.sql` is seed data for a demo database: six kegs on eight taps
  and a US display unit system, data only, loaded on top of
  `internal/store/schema.sql`. Its keg 1 shares the capture's token. Its
  history is generated by a recursive query when the file is loaded: 30 days
  at five-minute steps, ending at the load time and at each keg's current
  reading, from plain arithmetic so every load gives the same shape. A second
  query records the same pours in `pours`, plus a few from a deleted scale
  that only show on All Pours.
- CI (`.github/workflows/ci.yaml`) checks `gofmt`, runs `go vet`,
  `go test -race ./...` and `go build ./...`.
- Releases (`.github/workflows/release.yaml`) run on every push to `main`: the
  tests again, then a multi-arch Docker image (`linux/amd64`, `linux/arm64`)
  pushed to `ghcr.io/matt-freed/open-plaato-keg`.
- `docker-compose.yaml` runs that image with `./data` mounted at `/db`, which
  holds the database. The container runs as uid 10001, so on Linux the
  directory must be writable by that user.

## Known limitations

- **Averaged long history.** On ranges averaged into steps, the History page's
  temperature average and range come from the averaged points, so the range is
  narrower than the true extremes. Rows older than `LOG_COMPACT_AFTER_DAYS`
  are hourly averages in the database and in CSV exports; the minute detail is
  gone.
- **No pour backfill.** Pours are recorded from when this server version first
  sees a pouring window; the minute history from before it is not converted.
- **Shutdown mid-pour.** For the reason below, a pour in progress at shutdown is
  not ended. Its start is stored, so it ends, and is recorded, when the keg
  reconnects and reports the flag off.
- **Amount settling after the flag.** A pour is sized from the amount left when
  the flag clears. Firmware that cleared pin 49 before sending its settled
  reading would under-report the pour.

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
- **UI reconnection.** The Keg Setup page does not reconnect its WebSocket;
  the tap list and the Kegs page do.
- **Tap order across screens.** A drag-and-drop reorder publishes no event, so
  other open tap lists pick up the new order only at their next minute reload.
- **BarHelper time on the Kegs page.** Recording a send publishes no event, so
  the new time appears at the scale's next update or the page's minute reload.
  A keg removed from `BARHELPER_KEG_MONITOR_MAPPING` keeps showing its last
  send time.
- **Kegs without a tap.** The tap list shows taps, so a keg that no tap links to
  appears only on the Kegs page.
