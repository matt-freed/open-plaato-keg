# Open Plaato Keg

Take control of your Plaato Keg. This is a local replacement for the
discontinued Plaato cloud service: it speaks the Blynk protocol your keg scale
already uses, so the hardware connects to your own server instead, with no
firmware changes.

A Go port of the original [Elixir
implementation](https://github.com/DarkJaeger/open-plaato-keg). 

## How it works

The Plaato Keg was built on the Blynk IoT platform. It opens a TCP connection
to a configured host and port and pushes its readings as Blynk "virtual pin"
writes. This server decodes those writes and exposes them however you want to
consume them.

```mermaid
graph LR
    A(Plaato Keg) --> B{open-plaato-keg};
    B <--> C[Web UI];
    B <--> D[REST API];
    B --> E[WebSocket];
    B --> F[BarHelper];
```

## Features

- **Keg monitoring** — remaining volume, temperature, pour detection, battery
  of scale diagnostics
- **Keg control** — tare, calibrate against a known weight, set the empty keg
  weight and full volume, switch units, choose beer or CO₂ mode, set pour
  sensitivity
- **Tap list** — a display-ready page for what is on tap, with per-tap artwork
- **Beverage library** — reusable beer records to attach to taps
- **History** — per-keg time series with charts and CSV export
- **REST API and WebSocket** — everything the UI does, available to your own
  tooling
- **BarHelper** — forwards volume readings to the [BarHelper custom keg
  monitor](https://docs.barhelper.app/english/settings/custom-keg-monitor) API

## Pointing a keg at this server

The keg is configured through its own setup page; nothing about this server
needs to be registered anywhere.

1. Power on the keg. All three LEDs light up and blink slowly.
2. Turn it over and remove the yellow "Reset Key" from the bottom.
3. Hold the Reset Key in the hole marked "Reset" for about five seconds. A weak
   fridge magnet works too. The LEDs turn off and come back on.
4. Connect to the `PLAATO-XXXXX` Wi-Fi network the keg now broadcasts and open
   <http://192.168.4.1>.
5. Fill in:
   - **WiFi SSID** and **Password** — 2.4 GHz only; the keg has no 5 GHz radio
   - **Auth token** — a 32-character hex string (digits and lowercase `a`–`f`)
     of your choosing. This becomes the keg's id, so give each keg its own.
   - **Host** and **Port** — the address of this server and `KEG_LISTENER_PORT`

The same thing without the form:

```
http://192.168.4.1/config?ssid=My+Wifi&pass=my_password&blynk=00000000000000000000000000000001&host=192.168.0.123&port=4545
```

The keg appears in the web UI as soon as it connects and sends its first
reading.

## Running it

### Docker

```bash
docker run -d --name open-plaato-keg \
  -p 4545:4545 -p 8085:8085 \
  -v "$PWD/data:/db" \
  ghcr.io/matt-freed/open-plaato-keg:latest
```

The `-v` is not optional if you want your data to survive a container update.
Images are published for `linux/amd64` and `linux/arm64`, so a Raspberry Pi 4/5
on a 64-bit OS works.

### Docker Compose

See [docker-compose.yaml](docker-compose.yaml).

```bash
docker compose up -d
```

### From source

Requires Go 1.25 or newer. There is no C toolchain requirement: the SQLite
driver is pure Go.

```bash
go build ./cmd/open-plaato-keg
./open-plaato-keg
```

Then open <http://localhost:8085>.

## Configuration

Everything is configured through the environment. An invalid value stops the
server at startup rather than being silently ignored.

| Variable | Default | Description |
|---|---|---|
| `KEG_LISTENER_PORT` | `4545` | TCP port the keg hardware connects to |
| `HTTP_LISTENER_PORT` | `8085` | Port for the web UI, REST API and WebSocket |
| `DATABASE_FILE_PATH` | `/db/open-plaato-keg.db` | SQLite database. Uploaded images are stored beside it. |
| `INCLUDE_UNKNOWN_DATA` | `false` | Keep virtual pins this server does not recognise, under the keg's `extra` field |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `BARHELPER_ENABLED` | `false` | Forward volume readings to BarHelper |
| `BARHELPER_ENDPOINT` | BarHelper's custom keg monitor URL | Override for testing |
| `BARHELPER_API_KEY` | — | Required when BarHelper is enabled |
| `BARHELPER_UNIT` | `l` | Unit sent to BarHelper; `l` is litres |
| `BARHELPER_KEG_MONITOR_MAPPING` | — | `<auth-token>:<monitor-id>` pairs, comma separated. A keg that is not listed is never forwarded. |

The API key and the monitor ids come from BarHelper's own [custom keg monitor
settings](https://docs.barhelper.app/english/settings/custom-keg-monitor).
Create a monitor there for each tap, then map each keg's auth token to the
monitor id it should update.

## The web UI

Plain HTML and JavaScript with no build step, embedded into the binary. `/`
redirects to whichever page is set as home — the tap list unless you change it.

| Page | Description |
|---|---|
| `/taplist.html` | **Tap List** — the display page, meant to be left on a screen. A card per tap: handle artwork, beer name, brewery, style, ABV and IBU badges, tasting notes, and how much is left in the linked keg. Live clock, WebSocket updates, and a full reload every minute. |
| `/index.html` | **Kegs** — a card per scale, showing remaining volume or percentage, temperature, last pour and a pouring indicator, drawn as a keg or a CO₂ cylinder depending on the mode. Drag the cards to reorder them; the × forgets a scale and its history. |
| `/setup.html` | **Scale Setup** — everything the device can be told: units and weight-or-volume display, tare, calibration against a known weight, empty keg weight, full volume, temperature offset and pour sensitivity, plus beer or CO₂ mode. Also shows scale information and connection status. Needs the keg to be connected. |
| `/history.html` | **History** — pick a keg and a range from 1h to 30d for a chart of its readings, with the same data as a CSV download. |
| `/taplist-setup.html` | **Tap List Setup** — the tap editor: tap number, beer details, the keg the tap draws from, its handle image and an open-tap display id. Fields can be auto-filled from the beverage library, and the kegged date is written to the device. |
| `/beverages.html` | **Beverage Library** — reusable beer records, including gravities, SRM and where the recipe came from, to load into a tap later. ABV is worked out from OG and FG when it is not given. |
| `/tap-handles.html` | **Tap Handles** — upload and delete the artwork used on the tap list. Each image must be a JPEG of exactly 200×200 pixels. |
| `/dashboard-setup.html` | **Dashboard Setup** — appearance and preferences for every page: accent, page, card and text colours, fonts (with separate tap list title and body faces), a full-page background image with an adjustable dark overlay, which page is home, and a 12- or 24-hour clock. |

## API

All responses are JSON with real types: numbers are numbers, `is_pouring` is a
boolean, and a reading the device has never sent is `null` rather than zero.

### Kegs

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/kegs` | Every keg, ordered for display |
| `GET` | `/api/kegs/devices` | Just the ids |
| `GET` | `/api/kegs/connected` | Ids with a live TCP connection |
| `GET` | `/api/kegs/{id}` | One keg |
| `POST` | `/api/kegs/order` | `{"ordered_ids": [...]}` — set the display order |
| `POST` | `/api/kegs/{id}/delete` | Forget a keg and its history |
| `GET` | `/api/kegs/{id}/log?range=1h\|6h\|24h\|7d\|30d` | History |
| `GET` | `/api/kegs/{id}/log/csv?range=…` | The same, as CSV |

### Keg commands

All take `POST` and return `503 not_connected` when the keg is offline, except
where noted.

| Path | Body | Notes |
|---|---|---|
| `/api/kegs/{id}/tare` | — | Momentary; follow with `tare-release` |
| `/api/kegs/{id}/tare-release` | — | |
| `/api/kegs/{id}/empty-keg` | — | Momentary; follow with `empty-keg-release` |
| `/api/kegs/{id}/empty-keg-release` | — | |
| `/api/kegs/{id}/empty-keg-weight` | `{"value": 4.5}` | |
| `/api/kegs/{id}/max-keg-volume` | `{"value": 19.0}` | |
| `/api/kegs/{id}/temperature-offset` | `{"value": -7.5}` | |
| `/api/kegs/{id}/calibrate-known-weight` | `{"value": 5.0}` | |
| `/api/kegs/{id}/unit` | `{"value": "metric"\|"us"}` | |
| `/api/kegs/{id}/measure-unit` | `{"value": "weight"\|"volume"}` | |
| `/api/kegs/{id}/keg-mode` | `{"value": "beer"\|"co2"}` | |
| `/api/kegs/{id}/sensitivity` | `{"value": "very_low"\|"low"\|"medium"\|"high"}` | |
| `/api/kegs/{id}/beer-style` | `{"value": "Saison"}` | Stored locally too; the device never reports this back |
| `/api/kegs/{id}/date` | `{"value": "12.01.2025"}` | As above |
| `/api/kegs/{id}/label` | `{"value": "Kitchen tap"}` | Stored here only |
| `/api/kegs/{id}/display-mode` | `{"value": "weight_primary"\|"percent_primary"}` | Stored here only |
| `/api/kegs/{id}/og`, `/fg`, `/co2-capacity` | `{"value": 1.050}` | Stored here only |
| `/api/kegs/{id}/abv` | `{"og": 1.050, "fg": 1.010}` | Computes and stores the strength |
| `/api/kegs/{id}/reset-last-pour` | — | Stored here only |

Numeric values may be sent as JSON numbers or as strings.

### Taps

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/taps` | Every tap, by tap number, unnumbered ones last |
| `GET` | `/api/taps/{id}` | One tap |
| `POST` | `/api/taps/new` | Create one; the response carries the generated id |
| `POST` | `/api/taps/{id}` | Save one |
| `POST` | `/api/taps/{id}/delete` | Delete one |

A tap body takes these fields, all optional:

| Field | Type | Notes |
|---|---|---|
| `tap_number` | number | Orders the tap list; `null` sorts last |
| `name`, `brewery`, `style` | string | |
| `abv`, `ibu` | number | |
| `color` | string | Accent for the tap card; defaults to `#c9a849` |
| `description`, `tasting_notes` | string | |
| `expiration_date` | string | Shown as given; not parsed |
| `keg_id` | string | The keg this tap draws from, so the card can show what is left |
| `handle_image` | string | A filename from `/api/tap-handles` |
| `device_id` | string | Binds an open-tap display; truncated to 6 characters |

### Beverages

Reusable beer records, kept apart from the taps so the same beer can be put
back on later.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/beverages` | Every saved beverage |
| `GET` | `/api/beverages/{id}` | One beverage |
| `POST` | `/api/beverages/new` | Create one |
| `POST` | `/api/beverages/{id}` | Save one; `created_at` is kept |
| `POST` | `/api/beverages/{id}/delete` | Delete one |

| Field | Type | Notes |
|---|---|---|
| `name`, `brewery`, `style` | string | |
| `abv`, `ibu` | number | `abv` is estimated from `og` and `fg` when omitted |
| `color` | string | |
| `description`, `tasting_notes` | string | |
| `og`, `fg`, `srm` | number | |
| `source` | string | Where the recipe came from |

### Tap handles

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/tap-handles` | Uploaded handles, newest first |
| `POST` | `/api/tap-handles/upload` | Multipart, field `image`. JPEG, exactly 200×200, 10 MB at most |
| `POST` | `/api/tap-handles/{filename}/delete` | Delete the record and the file |
| `GET` | `/uploads/tap-handles/{filename}` | The image itself |

The stored filename is generated rather than taken from the upload, so it can
be used as given in a tap's `handle_image`.

### Settings

| Method | Path | Body | Description |
|---|---|---|---|
| `GET` | `/api/config` | — | Home page, clock format and theme together |
| `GET` `POST` | `/api/config/home-page` | `{"home_page": "taplist"\|"kegs"}` | Where `/` sends the browser |
| `GET` `POST` | `/api/config/time-format` | `{"time_format": "12h"\|"24h"}` | Clock and timestamps |
| `GET` `POST` | `/api/config/theme` | A theme object | Colours and fonts |
| `POST` | `/api/uploads/background` | Multipart, field `image` | JPEG, PNG, WebP or GIF. Replaces any existing background |
| `DELETE` | `/api/uploads/background` | — | Remove it |
| `GET` | `/uploads/background` | — | The image itself |
| `GET` | `/theme.css` | — | The stored theme as CSS custom properties, which `style.css` consumes |
| `GET` | `/api/alive` | — | Status and server version |

An unrecognised `home_page` or `time_format` falls back to the default rather
than being rejected. The theme accepts `accent_color`, `bg_color`, `card_bg`,
`text_color`, `font_family`, `taplist_title_font`, `taplist_body_font`,
`bg_image` and `bg_opacity` (`0`–`1`); anything else is ignored, and a value
that could break out of the stylesheet is dropped. A named font is fetched from
Google Fonts.

### Displays

`GET /get_keg/{device_id}` serves an
[open-tap](https://github.com/pcurylo/open-tap) ESP32 display. It looks up the
tap bound to that device id and answers with the field names that firmware
expects, including an absolute URL for the handle image and the total weight on
the scale.

### WebSocket

`GET /ws`. Every frame is tagged:

```json
{"type": "keg", "data": { ... }}
{"type": "keg_removed", "id": "0000...0001"}
```

The current state of every keg is sent on connect, so a page does not have to
wait for the next update. Clients do not send anything.

## Development

```bash
go test ./...          # everything
go test -race ./...    # what CI runs
```

The test suite includes a recorded session from real hardware
(`testdata/capture`, 117 TCP segments). It is replayed through the framer, the
pin decoder, and the TCP server itself, which is what keeps the wire protocol
honest.

To drive a running server with that same recording:

```bash
go run ./cmd/open-plaato-keg &
go run ./cmd/kegsim -addr localhost:4545
```

`kegsim` waits for each acknowledgement before sending the next segment, the
same way the firmware does, and fails loudly if one is missing or carries the
wrong message id.

### The protocol

```
inbound frame : [cmd u8][msg_id u16be][len u16be][body len bytes]
server ack    : [0x00][msg_id u16be][0x00 0xC8]        -- exactly 5 bytes

login     : cmd 29 get_shared_dash, body = the 32-char auth token
            (cmd 2 login on older firmware)
internal  : cmd 17, body = "key\0value\0key\0value\0..."
sync      : cmd 16, body = "vr\0<pin>\0<pin>..."   (device asking us)
pin write : cmd 20, body = "vw\0<pin>\0<value>"
property  : cmd 19, body = "<pin>\0<property>\0<value>"
ping      : cmd 6, no body
```

Two details the hardware depends on:

- **The acknowledgement must echo the message id.** Older firmware checks this
  before it will consider itself connected.
- **One acknowledgement per TCP segment, not per message.** The firmware
  coalesces several messages into one segment and expects a single reply,
  carrying the first message's id.

Virtual pin numbers are documented by Plaato
[here](https://intercom.help/plaato/en/articles/5004722-pins-plaato-keg); the
mapping this server uses is in `internal/plaato/pins.go`.

## What this does not do

The Elixir original also supported Plaato Airlocks, transfer scales, MQTT,
Grainfather, Brewfather, OTA firmware serving and container self-updates. None
of that is here. Airlocks speak the same protocol and will connect, but nothing
is stored for them.
