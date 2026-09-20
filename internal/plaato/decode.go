package plaato

import (
	"bytes"
	"fmt"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
)

// Property is one decoded virtual-pin value.
type Property struct {
	Name string
	// Value is a string, float64, int64 or bool according to the pin's Kind.
	Value any
	// Raw is the value exactly as the device sent it, kept for logging.
	Raw string
	// Transient marks a value that should not be persisted (see Pin.Transient).
	Transient bool
}

// Packet is everything decoded from one batch of frames.
type Packet struct {
	// DeviceID is the 32-character auth token, set when the batch contained a
	// login frame.
	DeviceID string
	// Props are in wire order. A batch may carry the same pin more than once,
	// in which case the later value is the current one.
	Props []Property
	// Internal holds the device metadata map (ver, fw, dev, build, tmpl,
	// h-beat, buff-in), set when the batch contained an internal frame.
	Internal map[string]string
	// Unknown holds values from pins that are not in the registry, keyed
	// "_hardware_vw_<pin>". Only populated when includeUnknown is set.
	Unknown map[string]string
}

// splitBody splits a NUL-separated Blynk body, discarding empty fields.
//
// Discarding empties matches the original Elixir implementation's
// String.split(trim: true) and matters in two places: internal bodies carry a
// trailing NUL, and a pin write with an empty value collapses to two fields and
// is dropped rather than storing an empty string.
func splitBody(body []byte) []string {
	parts := bytes.Split(body, []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}

// Decode interprets a batch of Blynk frames as Plaato data.
//
// includeUnknown mirrors the INCLUDE_UNKNOWN_DATA setting: when false,
// unmapped pins are discarded.
func Decode(frames []blynk.Frame, includeUnknown bool) Packet {
	var pkt Packet

	for _, f := range frames {
		switch f.Cmd {
		// Current Keg firmware announces its auth token with get_shared_dash;
		// older firmware uses login. Accept both.
		case blynk.CmdGetSharedDash, blynk.CmdLogin:
			if id := string(bytes.TrimSpace(f.Body)); id != "" {
				pkt.DeviceID = id
			}

		case blynk.CmdInternal:
			fields := splitBody(f.Body)
			if pkt.Internal == nil {
				pkt.Internal = make(map[string]string, len(fields)/2)
			}
			// An odd trailing field has no value and is ignored.
			for i := 0; i+1 < len(fields); i += 2 {
				pkt.Internal[fields[i]] = fields[i+1]
			}

		case blynk.CmdHardware, blynk.CmdProperty:
			fields := splitBody(f.Body)
			// Only the three-field form carries a value: "vw", pin, value for a
			// hardware write, or pin, property, value for a property write.
			if len(fields) != 3 {
				continue
			}
			pkt.decodePinWrite(f.Cmd, fields[0], fields[1], fields[2], includeUnknown)

		// hardware_sync is the device asking us for stored values, ping is a
		// keepalive; both are acknowledged by the caller and carry no data.
		default:
		}
	}

	return pkt
}

func (pkt *Packet) decodePinWrite(cmd blynk.Command, first, second, value string, includeUnknown bool) {
	var (
		pin Pin
		ok  bool
	)
	switch {
	case cmd == blynk.CmdHardware && first == "vw":
		pin, ok = hardwarePins[second]
	case cmd == blynk.CmdProperty:
		pin, ok = propertyPins[[2]string{first, second}]
	default:
		// A "vr" read request, or a property we do not track.
		return
	}

	if !ok {
		if includeUnknown {
			if pkt.Unknown == nil {
				pkt.Unknown = make(map[string]string)
			}
			pkt.Unknown[fmt.Sprintf("_%s_%s_%s", cmd, first, second)] = value
		}
		return
	}

	parsed, valid := parseValue(pin, value)
	if !valid {
		return
	}
	pkt.Props = append(pkt.Props, Property{
		Name:      pin.Name,
		Value:     parsed,
		Raw:       value,
		Transient: pin.Transient,
	})
}

// Get returns the last value decoded for name, which is the current one when a
// batch carried the same pin more than once.
func (pkt Packet) Get(name string) (any, bool) {
	for i := len(pkt.Props) - 1; i >= 0; i-- {
		if pkt.Props[i].Name == name {
			return pkt.Props[i].Value, true
		}
	}
	return nil, false
}

// Float returns the last float value decoded for name.
func (pkt Packet) Float(name string) (float64, bool) {
	v, ok := pkt.Get(name)
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

// Has reports whether the batch carried any value for name.
func (pkt Packet) Has(name string) bool {
	_, ok := pkt.Get(name)
	return ok
}
