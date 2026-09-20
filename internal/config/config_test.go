package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KegListenerPort != 4545 {
		t.Errorf("KegListenerPort = %d, want 4545", cfg.KegListenerPort)
	}
	if cfg.HTTPListenerPort != 8085 {
		t.Errorf("HTTPListenerPort = %d, want 8085", cfg.HTTPListenerPort)
	}
	if cfg.IncludeUnknownData {
		t.Error("IncludeUnknownData should default to false")
	}
	if cfg.BarHelper.Enabled {
		t.Error("BarHelper should default to disabled")
	}
	if cfg.BarHelper.Unit != "l" {
		t.Errorf("BarHelper.Unit = %q, want l", cfg.BarHelper.Unit)
	}
}

func TestDataDirDerivedFromDatabasePath(t *testing.T) {
	t.Setenv("DATABASE_FILE_PATH", "/db/open-plaato-keg.db")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.DataDir(); got != "/db" {
		t.Errorf("DataDir = %q, want /db", got)
	}
	if got := cfg.TapHandleDir(); got != "/db/tap-handles" {
		t.Errorf("TapHandleDir = %q, want /db/tap-handles", got)
	}
}

func TestEnvBool(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "1", "yes", "on"} {
		t.Setenv("INCLUDE_UNKNOWN_DATA", v)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if !cfg.IncludeUnknownData {
			t.Errorf("%q did not parse as true", v)
		}
	}
	for _, v := range []string{"false", "0", "no", "off"} {
		t.Setenv("INCLUDE_UNKNOWN_DATA", v)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if cfg.IncludeUnknownData {
			t.Errorf("%q did not parse as false", v)
		}
	}
}

// A typo must fail loudly rather than silently disabling a feature, which is
// what the Elixir implementation did (only the exact string "true" was true).
func TestEnvBoolRejectsGarbage(t *testing.T) {
	t.Setenv("BARHELPER_ENABLED", "ture")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted BARHELPER_ENABLED=ture")
	}
}

func TestEnvIntRejectsBadPort(t *testing.T) {
	t.Setenv("KEG_LISTENER_PORT", "not-a-port")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a non-numeric port")
	}
	t.Setenv("KEG_LISTENER_PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an out-of-range port")
	}
}

func TestBarHelperMonitorMapping(t *testing.T) {
	t.Setenv("BARHELPER_KEG_MONITOR_MAPPING", "token-a:monitor-1, token-b:monitor-2")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.BarHelper.Monitors["token-a"]; got != "monitor-1" {
		t.Errorf("token-a maps to %q, want monitor-1", got)
	}
	if got := cfg.BarHelper.Monitors["token-b"]; got != "monitor-2" {
		t.Errorf("token-b maps to %q, want monitor-2", got)
	}
}

func TestBarHelperMappingRejectsMalformedPair(t *testing.T) {
	t.Setenv("BARHELPER_KEG_MONITOR_MAPPING", "token-without-a-monitor")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a mapping entry with no colon")
	}
}

// Enabling the integration without a key would fail silently on every send.
func TestBarHelperEnabledRequiresAPIKey(t *testing.T) {
	t.Setenv("BARHELPER_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted BARHELPER_ENABLED without an API key")
	}
	t.Setenv("BARHELPER_API_KEY", "secret")
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}
