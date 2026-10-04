// Package config reads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	// KegListenerPort is the TCP port Plaato Keg hardware connects to.
	KegListenerPort int
	// HTTPListenerPort serves the web UI, REST API and WebSocket.
	HTTPListenerPort int
	// DatabaseFilePath is the SQLite database.
	DatabaseFilePath string
	// IncludeUnknownData reports unmapped virtual pins in the keg's extra data
	// instead of discarding them.
	IncludeUnknownData bool
	// LogRetention is how long keg history is kept before it is pruned. Zero
	// keeps it forever.
	LogRetention time.Duration
	// LogCompactAfter is how long keg history keeps every reading before
	// older readings are combined into hourly averages. Zero never combines.
	LogCompactAfter time.Duration

	BarHelper BarHelperConfig
}

// BarHelperConfig configures the outbound BarHelper integration.
type BarHelperConfig struct {
	Enabled  bool
	Endpoint string
	APIKey   string
	// Unit is sent as the payload's "type" field; "l" means litres.
	Unit string
	// Monitors maps a Plaato auth token to a BarHelper keg monitor id. A keg
	// that is not in the map is never forwarded.
	Monitors map[string]string
}

// Load reads the configuration from the environment, applying defaults.
//
// An invalid value is an error rather than a silent fallback: a typo in
// BARHELPER_ENABLED should not quietly disable the integration.
func Load() (Config, error) {
	var (
		cfg Config
		err error
	)

	if cfg.KegListenerPort, err = envInt("KEG_LISTENER_PORT", 4545); err != nil {
		return cfg, err
	}
	if cfg.HTTPListenerPort, err = envInt("HTTP_LISTENER_PORT", 8085); err != nil {
		return cfg, err
	}
	cfg.DatabaseFilePath = envString("DATABASE_FILE_PATH", "/db/open-plaato-keg.db")
	if cfg.IncludeUnknownData, err = envBool("INCLUDE_UNKNOWN_DATA", false); err != nil {
		return cfg, err
	}
	if cfg.LogRetention, err = envDays("LOG_RETENTION_DAYS", 365); err != nil {
		return cfg, err
	}
	if cfg.LogCompactAfter, err = envDays("LOG_COMPACT_AFTER_DAYS", 30); err != nil {
		return cfg, err
	}

	if cfg.BarHelper.Enabled, err = envBool("BARHELPER_ENABLED", false); err != nil {
		return cfg, err
	}
	cfg.BarHelper.Endpoint = envString("BARHELPER_ENDPOINT",
		"https://europe-west1-barhelper-app.cloudfunctions.net/api/customKegMon")
	cfg.BarHelper.APIKey = envString("BARHELPER_API_KEY", "")
	cfg.BarHelper.Unit = envString("BARHELPER_UNIT", "l")
	if cfg.BarHelper.Monitors, err = envKeyValueCSV("BARHELPER_KEG_MONITOR_MAPPING"); err != nil {
		return cfg, err
	}

	if cfg.BarHelper.Enabled && cfg.BarHelper.APIKey == "" {
		return cfg, fmt.Errorf("BARHELPER_ENABLED is set but BARHELPER_API_KEY is empty")
	}

	return cfg, nil
}

func envString(name, def string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) (int, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number", name, v)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("%s: %d is not a valid port", name, n)
	}
	return n, nil
}

// maxRetentionDays keeps the retention well inside what a time.Duration can
// hold, which is about 292 years.
const maxRetentionDays = 36500

// envDays parses a whole number of days. Zero is allowed and means "forever".
func envDays(name string, def int) (time.Duration, error) {
	days := def
	if v, ok := os.LookupEnv(name); ok && v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("%s: %q is not a whole number of days", name, v)
		}
		if n < 0 || n > maxRetentionDays {
			return 0, fmt.Errorf("%s: %d is not between 0 and %d", name, n, maxRetentionDays)
		}
		days = n
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

func envBool(name string, def bool) (bool, error) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return def, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s: %q is not a boolean", name, v)
}

// envKeyValueCSV parses "key:value,key:value" into a map.
func envKeyValueCSV(name string) (map[string]string, error) {
	out := map[string]string{}
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return out, nil
	}
	for _, pair := range strings.Split(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, found := strings.Cut(pair, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !found || key == "" || value == "" {
			return nil, fmt.Errorf("%s: %q is not a key:value pair", name, pair)
		}
		out[key] = value
	}
	return out, nil
}
