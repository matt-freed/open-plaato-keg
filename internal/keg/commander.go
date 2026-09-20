package keg

import (
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/plaato"
)

// Commander sends commands to connected kegs.
type Commander struct {
	registry *Registry
}

// NewCommander returns a commander backed by the given registry.
func NewCommander(r *Registry) *Commander { return &Commander{registry: r} }

// nextMsgID returns a message id in 1..65535. The device echoes it back and
// treats zero as unset, so it is excluded.
func nextMsgID() uint16 { return uint16(rand.Intn(65535) + 1) }

// WritePin writes a value to a virtual pin: "vw\0<pin>\0<value>".
func (c *Commander) WritePin(kegID, pin, value string) error {
	body := strings.Join([]string{"vw", pin, value}, "\x00")
	return c.send(kegID, blynk.NewCommand(blynk.CmdHardware, nextMsgID(), []byte(body)))
}

// SyncPins asks the device to report the current value of each pin:
// "vr\0<pin>\0<pin>...".
func (c *Commander) SyncPins(kegID string, pins ...string) error {
	body := strings.Join(append([]string{"vr"}, pins...), "\x00")
	return c.send(kegID, blynk.NewCommand(blynk.CmdHardwareSync, nextMsgID(), []byte(body)))
}

// Disconnect closes a keg's connection. It is not an error if the keg is
// already gone.
func (c *Commander) Disconnect(kegID string) {
	conn, err := c.registry.Lookup(kegID)
	if err != nil {
		return
	}
	slog.Info("disconnecting keg by request", "keg", kegID)
	conn.Close()
}

// Connected reports whether the keg currently has a live connection.
func (c *Commander) Connected(kegID string) bool {
	_, err := c.registry.Lookup(kegID)
	return err == nil
}

// ConnectedIDs returns every connected keg's id.
func (c *Commander) ConnectedIDs() []string { return c.registry.IDs() }

func (c *Commander) send(kegID string, frame []byte) error {
	conn, err := c.registry.Lookup(kegID)
	if err != nil {
		return err
	}
	if err := conn.Send(frame); err != nil {
		return fmt.Errorf("send to keg %s: %w", kegID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Named commands
//
// Tare and empty-keg are momentary buttons on the device: it acts on the
// press and needs the matching release before it will fire again.
// ---------------------------------------------------------------------------

// Tare presses the tare button.
func (c *Commander) Tare(kegID string) error { return c.WritePin(kegID, plaato.PinTare, "1") }

// TareRelease releases the tare button.
func (c *Commander) TareRelease(kegID string) error { return c.WritePin(kegID, plaato.PinTare, "0") }

// SetEmptyKeg presses the "record empty keg weight" button.
func (c *Commander) SetEmptyKeg(kegID string) error {
	return c.WritePin(kegID, plaato.PinEmptyKegWeight, "1")
}

// SetEmptyKegRelease releases it.
func (c *Commander) SetEmptyKegRelease(kegID string) error {
	return c.WritePin(kegID, plaato.PinEmptyKegWeight, "0")
}

// SetEmptyKegWeight sets the stored empty keg weight.
func (c *Commander) SetEmptyKegWeight(kegID string, weight float64) error {
	return c.WritePin(kegID, plaato.PinEmptyKegWeight, formatFloat(weight))
}

// SetMaxKegVolume sets the keg's full volume.
func (c *Commander) SetMaxKegVolume(kegID string, volume float64) error {
	return c.WritePin(kegID, plaato.PinMaxKegVolume, formatFloat(volume))
}

// SetTemperatureOffset calibrates the temperature reading.
func (c *Commander) SetTemperatureOffset(kegID string, offset float64) error {
	return c.WritePin(kegID, plaato.PinTemperatureOffset, formatFloat(offset))
}

// CalibrateKnownWeight calibrates the scale against a known weight.
func (c *Commander) CalibrateKnownWeight(kegID string, weight float64) error {
	return c.WritePin(kegID, plaato.PinKnownWeight, formatFloat(weight))
}

// SetBeerStyle sets the style shown on the device.
//
// The leading space matches what the Plaato app sends; the device's display
// clips the first character without it.
func (c *Commander) SetBeerStyle(kegID, style string) error {
	return c.WritePin(kegID, plaato.PinBeerStyle, " "+style)
}

// SetDate sets the date shown on the device, with the same leading space.
func (c *Commander) SetDate(kegID, date string) error {
	return c.WritePin(kegID, plaato.PinDate, " "+date)
}

// Unit selects the metric or US unit system.
const (
	UnitMetric = 1
	UnitUS     = 2
)

// SetUnit selects the unit system.
func (c *Commander) SetUnit(kegID string, unit int) error {
	if unit != UnitMetric && unit != UnitUS {
		return fmt.Errorf("unit must be %d (metric) or %d (US), got %d", UnitMetric, UnitUS, unit)
	}
	return c.WritePin(kegID, plaato.PinUnit, strconv.Itoa(unit))
}

// Measure modes.
const (
	MeasureWeight = 1
	MeasureVolume = 2
)

// SetMeasureUnit selects whether the keg reports weight or volume.
func (c *Commander) SetMeasureUnit(kegID string, measure int) error {
	if measure != MeasureWeight && measure != MeasureVolume {
		return fmt.Errorf("measure unit must be %d (weight) or %d (volume), got %d", MeasureWeight, MeasureVolume, measure)
	}
	return c.WritePin(kegID, plaato.PinMeasureUnit, strconv.Itoa(measure))
}

// Keg modes.
const (
	ModeBeer = 1
	ModeCO2  = 2
)

// SetKegMode switches between beer and CO2 monitoring.
func (c *Commander) SetKegMode(kegID string, mode int) error {
	if mode != ModeBeer && mode != ModeCO2 {
		return fmt.Errorf("keg mode must be %d (beer) or %d (CO2), got %d", ModeBeer, ModeCO2, mode)
	}
	return c.WritePin(kegID, plaato.PinKegMode, strconv.Itoa(mode))
}

// SetSensitivity sets the pour detection sensitivity, 1 (lowest) to 4.
func (c *Commander) SetSensitivity(kegID string, level int) error {
	if level < 1 || level > 4 {
		return fmt.Errorf("sensitivity must be 1-4, got %d", level)
	}
	return c.WritePin(kegID, plaato.PinSensitivity, strconv.Itoa(level))
}

// formatFloat renders a value the way the device's own pin writes look:
// plain decimal, no exponent.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
