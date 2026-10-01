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
    -- most of these, and does not report back the ones it does.
    label                     TEXT    NOT NULL DEFAULT '',
    display_mode              TEXT    NOT NULL DEFAULT 'weight_primary',
    sort_order                INTEGER NOT NULL DEFAULT 0,
    beer_style                TEXT    NOT NULL DEFAULT '',
    keg_date                  TEXT    NOT NULL DEFAULT '',
    og                        REAL,
    fg                        REAL,
    abv                       REAL,
    co2_capacity              REAL,

    -- internal: the device metadata map (ver, fw, dev, build, tmpl, ...).
    -- extra: unmapped pins, only populated when INCLUDE_UNKNOWN_DATA is set.
    internal                  TEXT    NOT NULL DEFAULT '{}',
    extra                     TEXT    NOT NULL DEFAULT '{}',

    first_seen                INTEGER NOT NULL DEFAULT 0,
    last_seen                 INTEGER NOT NULL DEFAULT 0
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
