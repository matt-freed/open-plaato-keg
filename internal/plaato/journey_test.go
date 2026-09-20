package plaato

import (
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
)

// These frames were captured from the real Plaato phone app driving a keg, and
// are ported from the Elixir project's test/journeys suite. They are the record
// of what the hardware actually accepts, so the decoder must agree with them.
//
// Note the msg_ids are large and arbitrary rather than sequential: the app
// picks them at random, which is why the server has to echo them rather than
// count.

type journeyStep struct {
	desc  string
	cmd   blynk.Command
	msgID uint16
	body  string
	// want is the expected property, or an empty name when the frame carries
	// nothing we track.
	wantName  string
	wantValue any
}

func runJourney(t *testing.T, steps []journeyStep) {
	t.Helper()
	for _, s := range steps {
		t.Run(s.desc, func(t *testing.T) {
			pkt := Decode([]blynk.Frame{{Cmd: s.cmd, MsgID: s.msgID, Body: []byte(s.body)}}, false)
			if s.wantName == "" {
				if len(pkt.Props) != 0 {
					t.Fatalf("got %+v, want no properties", pkt.Props)
				}
				return
			}
			p := onlyProp(t, pkt)
			if p.Name != s.wantName {
				t.Fatalf("Name = %q, want %q", p.Name, s.wantName)
			}
			if p.Value != s.wantValue {
				t.Errorf("Value = %#v, want %#v", p.Value, s.wantValue)
			}
		})
	}
}

// Scale calibration and beer data, from test/journeys/keg_setup_test.exs.
func TestJourneyKegSetup(t *testing.T) {
	runJourney(t, []journeyStep{
		// Tare is a momentary button: press then release.
		{"tare press", blynk.CmdHardware, 19392, "vw\x0060\x001", "tare", "1"},
		{"tare release", blynk.CmdHardware, 9043, "vw\x0060\x000", "tare", "0"},

		{"empty keg press", blynk.CmdHardware, 13671, "vw\x0062\x001", "empty_keg_weight", 1.0},
		{"empty keg release", blynk.CmdHardware, 13671, "vw\x0062\x000", "empty_keg_weight", 0.0},

		{"max keg volume", blynk.CmdHardware, 31460, "vw\x0076\x0019.66", "max_keg_volume", 19.66},

		{"og", blynk.CmdHardware, 9289, "vw\x0065\x001055", "device_og", 1055.0},
		{"fg", blynk.CmdHardware, 19303, "vw\x0066\x001015", "device_fg", 1015.0},

		// The calculate button, and the two values the keg answers with.
		{"calculate press", blynk.CmdHardware, 22383, "vw\x0072\x001", "calculate", "1"},
		{"calculate release", blynk.CmdHardware, 32027, "vw\x0072\x000", "calculate", "0"},
		{"abv reply", blynk.CmdHardware, 22383, "vw\x0068\x005.403", "calculated_abv", 5.403},
		{"alcohol string reply", blynk.CmdHardware, 22383, "vw\x0070\x005.40%", "calculated_alcohol_string", "5.40%"},
	})
}

// Units, keg mode and sensitivity, from test/journeys/settings_test.exs.
func TestJourneySettings(t *testing.T) {
	runJourney(t, []journeyStep{
		{"unit metric", blynk.CmdHardware, 26206, "vw\x0071\x001", "unit", int64(1)},
		{"unit us", blynk.CmdHardware, 30662, "vw\x0071\x002", "unit", int64(2)},

		// Changing the measure unit makes the keg answer with a new volume bound.
		{"measure unit volume", blynk.CmdHardware, 26206, "vw\x0075\x002", "measure_unit", int64(2)},
		{"volume bound reply", blynk.CmdProperty, 265, "51\x00max\x0019.666", "max_keg_volume", 19.666},
		{"measure unit weight", blynk.CmdHardware, 4286, "vw\x0075\x001", "measure_unit", int64(1)},
		{"weight bound reply", blynk.CmdProperty, 299, "51\x00max\x0019.666", "max_keg_volume", 19.666},

		// Switching to beer mode relabels pin 47; we do not track widget labels.
		{"keg mode beer", blynk.CmdHardware, 27706, "vw\x0088\x001", "keg_mode", int64(1)},
		{"beer mode relabel", blynk.CmdProperty, 587, "47\x00label\x00Last pour", "", nil},

		// CO2 mode blanks pin 47's value and its label.
		{"keg mode co2", blynk.CmdHardware, 20409, "vw\x0088\x002", "keg_mode", int64(2)},
		{"co2 blanks last pour", blynk.CmdHardware, 675, "vw\x0047\x00 ", "last_pour_string", " "},
		{"co2 blanks label", blynk.CmdProperty, 676, "47\x00label\x00 ", "", nil},

		{"sensitivity very low", blynk.CmdHardware, 27292, "vw\x0089\x001", "sensitivity", int64(1)},
		{"sensitivity low", blynk.CmdHardware, 787, "vw\x0089\x002", "sensitivity", int64(2)},
		{"sensitivity medium", blynk.CmdHardware, 11020, "vw\x0089\x003", "sensitivity", int64(3)},
		{"sensitivity high", blynk.CmdHardware, 15756, "vw\x0089\x004", "sensitivity", int64(4)},
	})
}

// Beer metadata, from test/journeys/monitor_test.exs. The app prefixes these
// values with a space, which the decoder must preserve verbatim.
func TestJourneyMonitor(t *testing.T) {
	runJourney(t, []journeyStep{
		{"beer style", blynk.CmdHardware, 3800, "vw\x0064\x00 my style", "device_beer_style", " my style"},
		{"keg date", blynk.CmdHardware, 17522, "vw\x0067\x00 12.01.2025", "device_date", " 12.01.2025"},
	})
}

// Debug commands, from test/journeys/debug_test.exs.
func TestJourneyDebug(t *testing.T) {
	runJourney(t, []journeyStep{
		{"temperature offset", blynk.CmdHardware, 7071, "vw\x0052\x00-7.5", "temperature_offset", -7.5},
		{"known weight", blynk.CmdHardware, 23230, "vw\x0061\x000.1", "known_weight_calibrate", 0.1},
	})
}
