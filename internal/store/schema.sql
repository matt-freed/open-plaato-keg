-- Schema for open-plaato-keg. Applied idempotently at startup.
--
-- This file is the only place a column is declared. When a database created
-- by an older version is opened, any column below that its tables lack is
-- added automatically (store.addMissingColumns). A column added to an existing
-- table must therefore be one SQLite can add: nullable or with a constant
-- DEFAULT, not part of the primary key, and without UNIQUE, CHECK or
-- REFERENCES, which are not carried over. Renames, type changes, drops and
-- backfills still need a hand-written migration; a column removed from here is
-- simply left in place in older databases.

CREATE TABLE IF NOT EXISTS kegs (
    id                        TEXT PRIMARY KEY,

    -- Values reported by the device. NULL until the pin has been seen at least
    -- once, so "never reported" stays distinguishable from a genuine zero.
    amount_left               REAL,
    percent_of_beer_left      REAL,
    is_pouring                INTEGER,
    keg_temperature           REAL,
    last_pour                 REAL,
    last_pour_string          TEXT,
    temperature_offset        REAL,
    temperature_correction    REAL,
    weight_raw                REAL,
    volume_raw                REAL,
    pour_volume_raw           REAL,
    empty_keg_weight          REAL,
    max_keg_volume            REAL,
    min_temperature           REAL,
    max_temperature           REAL,
    min_temperature_max       REAL,
    max_temperature_min       REAL,
    unit                      INTEGER,   -- 1 metric, 2 US
    measure_unit              INTEGER,   -- 1 weight, 2 volume
    keg_mode                  INTEGER,   -- 1 beer, 2 CO2
    sensitivity               INTEGER,   -- 1..4
    weight_unit               TEXT,
    beer_left_unit_device     TEXT,      -- pin 74; superseded by the derived value
    volume_unit               TEXT,
    temperature_unit          TEXT,
    keg_temperature_string    TEXT,
    chip_temperature_string   TEXT,
    calculated_abv            REAL,
    calculated_alcohol_string TEXT,
    wifi_signal_strength      INTEGER,
    leak_detection            INTEGER,
    firmware_version          TEXT,
    device_og                 REAL,
    device_fg                 REAL,
    device_beer_style         TEXT,
    device_date               TEXT,

    -- Values set through the API and held only here. The device has no pin for
    -- any of these.
    label                     TEXT    NOT NULL DEFAULT '',
    sort_order                INTEGER NOT NULL DEFAULT 0,
    co2_capacity              REAL,

    -- internal: the device metadata map (ver, fw, dev, build, tmpl, ...).
    -- extra: unmapped pins, only populated when INCLUDE_UNKNOWN_DATA is set.
    internal                  TEXT    NOT NULL DEFAULT '{}',
    extra                     TEXT    NOT NULL DEFAULT '{}',

    -- first_seen: when the keg was first stored. last_seen: when the device last
    -- sent a data packet; changes made through the API do not move it.
    first_seen                INTEGER NOT NULL DEFAULT 0,
    last_seen                 INTEGER NOT NULL DEFAULT 0,
    -- When BarHelper last accepted a reading for this keg, or 0 if never.
    barhelper_last_sent       INTEGER NOT NULL DEFAULT 0,

    -- The pour in progress, held here so a restart mid-pour does not lose it.
    -- Both NULL while the keg is not pouring.
    pour_started_at           INTEGER,
    pour_start_amount         REAL
);

-- Time series of keg readings, written at most once per minute per keg.
CREATE TABLE IF NOT EXISTS keg_log (
    keg_id               TEXT    NOT NULL,
    ts                   INTEGER NOT NULL,   -- unix seconds
    amount_left          REAL,
    keg_temperature      REAL,
    percent_of_beer_left REAL,
    is_pouring           INTEGER,
    PRIMARY KEY (keg_id, ts)
) WITHOUT ROWID;

-- One row per detected pour. Never pruned: this is the long-term record.
CREATE TABLE IF NOT EXISTS pours (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    keg_id           TEXT    NOT NULL,
    started_at       INTEGER NOT NULL,   -- unix seconds
    ended_at         INTEGER NOT NULL,
    amount           REAL    NOT NULL,   -- in unit
    unit             TEXT    NOT NULL,   -- the keg's beer_left_unit when poured
    -- A copy of what was on tap, so editing the tap or kegging a new beer
    -- does not rewrite past pours.
    beer_name        TEXT    NOT NULL DEFAULT '',
    beer_style       TEXT    NOT NULL DEFAULT '',
    abv              REAL,
    tap_number       INTEGER,
    scale_label      TEXT    NOT NULL DEFAULT '',
    -- Set when the scale's history is cleared: the pour leaves that scale's
    -- history but stays in the list of all pours.
    hidden_from_keg  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS pours_by_time ON pours(ended_at);
CREATE INDEX IF NOT EXISTS pours_by_keg ON pours(keg_id, ended_at);

CREATE TABLE IF NOT EXISTS taps (
    id              TEXT PRIMARY KEY,
    tap_number      INTEGER,
    name            TEXT NOT NULL DEFAULT '',
    brewery         TEXT NOT NULL DEFAULT '',
    style           TEXT NOT NULL DEFAULT '',
    abv             REAL,
    ibu             REAL,
    -- Beer colour in SRM; the tap list renders the keg in this colour.
    srm             REAL,
    -- A named colour (see ColorPresets) for drinks SRM cannot describe, such as
    -- sparkling water. At most one of srm and color_preset is set.
    color_preset    TEXT NOT NULL DEFAULT '',
    -- Superseded by srm and color_preset; still the fallback for taps saved
    -- before they existed.
    color           TEXT NOT NULL DEFAULT '#c9a849',
    description     TEXT NOT NULL DEFAULT '',
    tasting_notes   TEXT NOT NULL DEFAULT '',
    -- When the beer was kegged, as YYYY-MM-DD, or ''. Held on the tap; the
    -- keg's own keg_date is not used for it.
    kegged_date     TEXT NOT NULL DEFAULT '',
    keg_id          TEXT NOT NULL DEFAULT ''
);


CREATE TABLE IF NOT EXISTS app_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
