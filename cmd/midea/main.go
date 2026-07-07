// Copyright (c) 2026 Andrew 'kondor' Sichevoi. All rights reserved.
// Use of this source code is governed by an MIT license found in the LICENSE file.

package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thekondor/midea-porta-split"
)

func main() {
	device := flag.String("device", "", "Device IP for local API, e.g. 192.168.1.100 (port 6444 assumed)")
	tokenHex := flag.String("token", "", "64-byte token as 128 hex chars (or MIDEA_TOKEN env)")
	keyHex := flag.String("key", "", "32-byte key as 64 hex chars (or MIDEA_KEY env)")
	deviceID := flag.Uint64("device-id", 0, "Device ID embedded in 5A5A packets (required)")
	debug := flag.Bool("debug", false, "Print raw request/response info to stderr")
	timeout := flag.Duration("timeout", 30*time.Second, "Overall operation timeout")

	flag.Usage = usage
	flag.Parse()

	// Env var fallback for credentials.
	if *tokenHex == "" {
		*tokenHex = os.Getenv("MIDEA_TOKEN")
	}
	if *keyHex == "" {
		*keyHex = os.Getenv("MIDEA_KEY")
	}

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	if *device == "" {
		fatalf("-device is required")
	}
	if *tokenHex == "" || *keyHex == "" {
		fatalf("-token and -key (or MIDEA_TOKEN/MIDEA_KEY) are required")
	}
	if *deviceID == 0 {
		fatalf("-device-id is required (obtain from UDP discovery or cloud API)")
	}

	token, err := hex.DecodeString(*tokenHex)
	if err != nil {
		fatalf("decode token: %v", err)
	}
	key, err := hex.DecodeString(*keyHex)
	if err != nil {
		fatalf("decode key: %v", err)
	}

	client, err := midea.NewClient(*device, token, key, *deviceID, midea.DefaultOptions())
	if err != nil {
		fatalf("create client: %v", err)
	}
	if *debug {
		client.SetDebugWriter(os.Stderr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	switch args[0] {
	case "poll":
		resp, err := client.Poll(ctx)
		if err != nil {
			fatalf("poll: %v", err)
		}
		printResponse(resp)

	case "set":
		req, err := parseSetFlags(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		resp, err := client.Update(ctx, req)
		if err != nil {
			fatalf("update: %v", err)
		}
		printResponse(resp)

	case "setprop":
		props, err := parseSetPropFlags(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := client.SetProperties(ctx, props); err != nil {
			fatalf("setprop: %v", err)
		}
		fmt.Println("ok")

	case "capabilities":
		caps, err := client.Capabilities(ctx)
		if err != nil {
			fatalf("capabilities: %v", err)
		}
		printCapabilities(caps)

	case "energy":
		e, err := client.Energy(ctx)
		if err != nil {
			fatalf("energy: %v", err)
		}
		printEnergy(e)

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %q\n", args[0])
		usage()
		os.Exit(1)
	}
}

func parseSetFlags(args []string) (midea.Request, error) {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: midea [global flags] set [options]")
		fmt.Fprintln(os.Stderr, "")
		fs.PrintDefaults()
	}

	on := fs.Bool("on", false, "Turn unit ON")
	off := fs.Bool("off", false, "Turn unit OFF")
	temp := fs.String("temp", "", "Target temperature in °C (e.g. 23.5)")
	modeStr := fs.String("mode", "", "Operation mode: auto|cool|dry|heat|fan")
	fanStr := fs.String("fan", "", "Fan speed: auto|silent|low|medium|high|full")
	swingV := fs.Bool("swing-v", false, "Enable vertical swing")
	noSwingV := fs.Bool("no-swing-v", false, "Disable vertical swing")
	swingH := fs.Bool("swing-h", false, "Enable horizontal swing")
	noSwingH := fs.Bool("no-swing-h", false, "Disable horizontal swing")
	eco := fs.Bool("eco", false, "Enable eco mode")
	noEco := fs.Bool("no-eco", false, "Disable eco mode")
	turbo := fs.Bool("turbo", false, "Enable turbo mode")
	noTurbo := fs.Bool("no-turbo", false, "Disable turbo mode")
	sleep := fs.Bool("sleep", false, "Enable sleep mode")
	noSleep := fs.Bool("no-sleep", false, "Disable sleep mode")
	display := fs.Bool("display", false, "Enable front panel display")
	noDisplay := fs.Bool("no-display", false, "Disable front panel display")
	frost := fs.Bool("frost", false, "Enable frost protect")
	noFrost := fs.Bool("no-frost", false, "Disable frost protect")
	comfort := fs.Bool("comfort", false, "Enable comfort mode")
	noComfort := fs.Bool("no-comfort", false, "Disable comfort mode")
	fahrenheit := fs.Bool("fahrenheit", false, "Set display unit to Fahrenheit")
	celsius := fs.Bool("celsius", false, "Set display unit to Celsius")
	beep := fs.Bool("beep", false, "Enable buzzer on command")
	noBeep := fs.Bool("no-beep", false, "Disable buzzer on command")
	followMe := fs.Bool("follow-me", false, "Enable follow-me mode")
	noFollowMe := fs.Bool("no-follow-me", false, "Disable follow-me mode")
	purifier := fs.Bool("purifier", false, "Enable purifier/anion")
	noPurifier := fs.Bool("no-purifier", false, "Disable purifier/anion")
	auxHeat := fs.Bool("aux-heat", false, "Enable auxiliary heat")
	noAuxHeat := fs.Bool("no-aux-heat", false, "Disable auxiliary heat")
	humidity := fs.Int("humidity", 0, "Target humidity 0–100 (0 = not set)")

	if err := fs.Parse(args); err != nil {
		return midea.Request{}, err
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var req midea.Request

	if set["on"] && *on {
		v := true
		req.Power = &v
	}
	if set["off"] && *off {
		v := false
		req.Power = &v
	}
	if set["temp"] {
		t, err := strconv.ParseFloat(*temp, 64)
		if err != nil {
			return midea.Request{}, fmt.Errorf("invalid -temp %q: %v", *temp, err)
		}
		req.TargetTemp = &t
	}
	if set["mode"] {
		m, err := parseMode(*modeStr)
		if err != nil {
			return midea.Request{}, err
		}
		req.Mode = &m
	}
	if set["fan"] {
		f, err := parseFan(*fanStr)
		if err != nil {
			return midea.Request{}, err
		}
		req.FanSpeed = &f
	}
	if set["swing-v"] && *swingV {
		v := true
		req.SwingV = &v
	}
	if set["no-swing-v"] && *noSwingV {
		v := false
		req.SwingV = &v
	}
	if set["swing-h"] && *swingH {
		v := true
		req.SwingH = &v
	}
	if set["no-swing-h"] && *noSwingH {
		v := false
		req.SwingH = &v
	}
	if set["eco"] && *eco {
		v := true
		req.Eco = &v
	}
	if set["no-eco"] && *noEco {
		v := false
		req.Eco = &v
	}
	if set["turbo"] && *turbo {
		v := true
		req.Turbo = &v
	}
	if set["no-turbo"] && *noTurbo {
		v := false
		req.Turbo = &v
	}
	if set["sleep"] && *sleep {
		v := true
		req.Sleep = &v
	}
	if set["no-sleep"] && *noSleep {
		v := false
		req.Sleep = &v
	}
	if set["display"] && *display {
		v := true
		req.Display = &v
	}
	if set["no-display"] && *noDisplay {
		v := false
		req.Display = &v
	}
	if set["frost"] && *frost {
		v := true
		req.FrostProtect = &v
	}
	if set["no-frost"] && *noFrost {
		v := false
		req.FrostProtect = &v
	}
	if set["comfort"] && *comfort {
		v := true
		req.ComfortMode = &v
	}
	if set["no-comfort"] && *noComfort {
		v := false
		req.ComfortMode = &v
	}
	if set["fahrenheit"] && *fahrenheit {
		v := true
		req.Fahrenheit = &v
	}
	if set["celsius"] && *celsius {
		v := false
		req.Fahrenheit = &v
	}
	if set["beep"] && *beep {
		v := true
		req.Beep = &v
	}
	if set["no-beep"] && *noBeep {
		v := false
		req.Beep = &v
	}
	if set["follow-me"] && *followMe {
		v := true
		req.FollowMe = &v
	}
	if set["no-follow-me"] && *noFollowMe {
		v := false
		req.FollowMe = &v
	}
	if set["purifier"] && *purifier {
		v := true
		req.Purifier = &v
	}
	if set["no-purifier"] && *noPurifier {
		v := false
		req.Purifier = &v
	}
	if set["aux-heat"] && *auxHeat {
		v := true
		req.AuxHeat = &v
	}
	if set["no-aux-heat"] && *noAuxHeat {
		v := false
		req.AuxHeat = &v
	}
	if set["humidity"] && *humidity > 0 {
		req.TargetHumidity = humidity
	}

	return req, nil
}

func parseMode(s string) (midea.Mode, error) {
	switch strings.ToLower(s) {
	case "auto":
		return midea.ModeAuto, nil
	case "cool":
		return midea.ModeCool, nil
	case "dry":
		return midea.ModeDry, nil
	case "heat":
		return midea.ModeHeat, nil
	case "fan":
		return midea.ModeFan, nil
	default:
		return 0, fmt.Errorf("unknown mode %q (valid: auto|cool|dry|heat|fan)", s)
	}
}

func parseFan(s string) (midea.FanSpeed, error) {
	switch strings.ToLower(s) {
	case "auto":
		return midea.FanAuto, nil
	case "silent":
		return midea.FanSilent, nil
	case "low":
		return midea.FanLow, nil
	case "medium":
		return midea.FanMedium, nil
	case "high":
		return midea.FanHigh, nil
	case "full":
		return midea.FanFull, nil
	default:
		return 0, fmt.Errorf("unknown fan speed %q (valid: auto|silent|low|medium|high|full)", s)
	}
}

func printResponse(r midea.Response) {
	unitLabel := "°C"
	tempConvert := midea.TempCelsius
	if r.Fahrenheit {
		unitLabel = "°F"
		tempConvert = midea.TempFahrenheit
	}
	fmt.Printf("power           = %v\n", r.Power)
	fmt.Printf("mode            = %s (%d)\n", r.Mode, int(r.Mode))
	fmt.Printf("target_temp     = %.1f%s\n", tempConvert(r.TargetTemp), unitLabel)
	if r.IndoorTemp != 0 {
		fmt.Printf("indoor_temp     = %.1f%s\n", tempConvert(r.IndoorTemp), unitLabel)
	} else {
		fmt.Printf("indoor_temp     = n/a\n")
	}
	if r.OutdoorTemp != 0 {
		fmt.Printf("outdoor_temp    = %.1f%s\n", tempConvert(r.OutdoorTemp), unitLabel)
	} else {
		fmt.Printf("outdoor_temp    = n/a\n")
	}
	if r.Humidity > 0 {
		fmt.Printf("humidity        = %d%%\n", r.Humidity)
	}
	if r.TargetHumidity > 0 {
		fmt.Printf("target_humidity = %d%%\n", r.TargetHumidity)
	}
	fmt.Printf("fan             = %s (%d)\n", r.FanSpeed, int(r.FanSpeed))
	fmt.Printf("swing_v         = %v\n", r.SwingV)
	fmt.Printf("swing_h         = %v\n", r.SwingH)
	fmt.Printf("eco             = %v\n", r.Eco)
	fmt.Printf("turbo           = %v\n", r.Turbo)
	fmt.Printf("sleep           = %v\n", r.Sleep)
	fmt.Printf("display         = %v\n", r.DisplayOn)
	fmt.Printf("frost_protect   = %v\n", r.FrostProtect)
	fmt.Printf("comfort_mode    = %v\n", r.ComfortMode)
	fmt.Printf("follow_me       = %v\n", r.FollowMe)
	fmt.Printf("purifier        = %v\n", r.Purifier)
	fmt.Printf("aux_heat        = %v\n", r.AuxHeat)
	fmt.Printf("fahrenheit      = %v\n", r.Fahrenheit)
	if r.FilterAlert {
		fmt.Printf("filter_alert    = true\n")
	}
	if r.Error {
		fmt.Printf("error           = true (code %d)\n", r.ErrCode)
	}
}

func printCapabilities(caps midea.Capabilities) {
	fmt.Printf("custom_fan_speed  = %v\n", caps.CustomFanSpeed)
	fmt.Printf("humidity_control  = %v\n", caps.HumidityControl)
	fmt.Printf("swing_angle       = %v\n", caps.SwingAngle)
	fmt.Printf("fresh_air         = %v\n", caps.FreshAir)
	fmt.Printf("breeze            = %v\n", caps.Breeze)
	fmt.Printf("purifier          = %v\n", caps.Purifier)
	fmt.Printf("out_silent        = %v\n", caps.OutSilent)
	if caps.MinCoolTemp != 0 || caps.MaxCoolTemp != 0 {
		fmt.Printf("cool_temp_range   = %.0f–%.0f°C\n", caps.MinCoolTemp, caps.MaxCoolTemp)
	}
	if caps.MinHeatTemp != 0 || caps.MaxHeatTemp != 0 {
		fmt.Printf("heat_temp_range   = %.0f–%.0f°C\n", caps.MinHeatTemp, caps.MaxHeatTemp)
	}
	if len(caps.Raw) > 0 {
		fmt.Printf("raw_caps          = %d entries\n", len(caps.Raw))
	}
}

func printEnergy(e midea.Energy) {
	fmt.Printf("total_kwh       = %.1f kWh\n", e.TotalKWh)
	fmt.Printf("current_run_kwh = %.1f kWh\n", e.CurrentRunKWh)
	fmt.Printf("realtime_kw     = %.3f kW\n", e.RealtimeKW)
}

func parseSetPropFlags(args []string) (midea.PropertiesRequest, error) {
	fs := flag.NewFlagSet("setprop", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: midea [global flags] setprop [options]")
		fmt.Fprintln(os.Stderr, "")
		fs.PrintDefaults()
	}

	swingVAngle := fs.Int("swing-v-angle", -1, "Vertical louver angle: 0 (off), 1, 25, 50, 75, 100")
	swingHAngle := fs.Int("swing-h-angle", -1, "Horizontal louver angle: 0 (off), 1, 25, 50, 75, 100")
	breezeStr := fs.String("breeze", "", "Breeze mode: off|away|mild|breezeless")
	freshAirStr := fs.String("fresh-air", "", "Fresh air fan: off|low|medium|high|boost")
	outSilent := fs.Bool("out-silent", false, "Enable outdoor unit silent mode")
	noOutSilent := fs.Bool("no-out-silent", false, "Disable outdoor unit silent mode")
	selfClean := fs.Bool("self-clean", false, "Trigger self-clean cycle")

	if err := fs.Parse(args); err != nil {
		return midea.PropertiesRequest{}, err
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var props midea.PropertiesRequest

	if set["swing-v-angle"] {
		a := midea.SwingAngle(*swingVAngle)
		props.SwingVAngle = &a
	}
	if set["swing-h-angle"] {
		a := midea.SwingAngle(*swingHAngle)
		props.SwingHAngle = &a
	}
	if set["breeze"] {
		b, err := parseBreeze(*breezeStr)
		if err != nil {
			return midea.PropertiesRequest{}, err
		}
		props.Breeze = &b
	}
	if set["fresh-air"] {
		f, err := parseFreshAir(*freshAirStr)
		if err != nil {
			return midea.PropertiesRequest{}, err
		}
		props.FreshAir = &f
	}
	if set["out-silent"] && *outSilent {
		v := true
		props.OutSilent = &v
	}
	if set["no-out-silent"] && *noOutSilent {
		v := false
		props.OutSilent = &v
	}
	if set["self-clean"] && *selfClean {
		v := true
		props.SelfClean = &v
	}

	return props, nil
}

func parseBreeze(s string) (midea.BreezeMode, error) {
	switch strings.ToLower(s) {
	case "off":
		return midea.BreezeOff, nil
	case "away":
		return midea.BreezeAway, nil
	case "mild":
		return midea.BreezeMild, nil
	case "breezeless":
		return midea.BreezeBreezeless, nil
	default:
		return 0, fmt.Errorf("unknown breeze mode %q (valid: off|away|mild|breezeless)", s)
	}
}

func parseFreshAir(s string) (midea.FreshAirSpeed, error) {
	switch strings.ToLower(s) {
	case "off":
		return midea.FreshAirOff, nil
	case "low":
		return midea.FreshAirLow, nil
	case "medium":
		return midea.FreshAirMedium, nil
	case "high":
		return midea.FreshAirHigh, nil
	case "boost":
		return midea.FreshAirBoost, nil
	default:
		return 0, fmt.Errorf("unknown fresh air speed %q (valid: off|low|medium|high|boost)", s)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `midea — Midea Porta Split AC unit CLI (V3 LAN protocol)

Usage:
  midea [global flags] <subcommand> [subcommand flags]

Global flags:
  -device HOST[:PORT]   Device IP (port 6444 assumed if omitted); required
  -token HEX            64-byte token as 128 hex chars (or MIDEA_TOKEN env)
  -key HEX              32-byte key as 64 hex chars (or MIDEA_KEY env)
  -device-id UINT       Device ID in 5A5A packets (default 0)
  -debug                Log raw bytes to stderr
  -timeout DURATION     Overall timeout (default 30s)

Subcommands:
  poll              Read current device state.
  set [options]     Apply one or more settings, print resulting state.
  setprop [options] Set advanced properties via 0xB0 command.
  capabilities      Query device feature capabilities (0xB5).
  energy            Query energy usage statistics.

Set options:
  -on / -off                    Power on/off
  -temp FLOAT                   Target temperature in °C (e.g. 23.5)
  -mode STRING                  auto|cool|dry|heat|fan
  -fan STRING                   auto|silent|low|medium|high|full
  -swing-v / -no-swing-v        Vertical swing (boolean)
  -swing-h / -no-swing-h        Horizontal swing (boolean)
  -eco / -no-eco                Eco mode
  -turbo / -no-turbo            Turbo/boost mode
  -sleep / -no-sleep            Sleep mode
  -display / -no-display        Front panel display
  -frost / -no-frost            Frost protect
  -comfort / -no-comfort        Comfort mode
  -fahrenheit / -celsius        Display unit
  -beep / -no-beep              Buzzer on command (default on)
  -follow-me / -no-follow-me    Follow-me mode
  -purifier / -no-purifier      Purifier/anion
  -aux-heat / -no-aux-heat      Auxiliary heat
  -humidity INT                 Target humidity 0-100

Setprop options:
  -swing-v-angle INT            Vertical louver: 0 (off), 1, 25, 50, 75, 100
  -swing-h-angle INT            Horizontal louver: 0 (off), 1, 25, 50, 75, 100
  -breeze STRING                off|away|mild|breezeless
  -fresh-air STRING             off|low|medium|high|boost
  -out-silent / -no-out-silent  Outdoor unit silent mode
  -self-clean                   Trigger self-clean cycle

Examples:
  export MIDEA_TOKEN=<128hexchars> MIDEA_KEY=<64hexchars>
  midea -device 192.168.1.100 poll
  midea -device 192.168.1.100 set -on -temp 23.0 -mode cool -fan auto
  midea -device 192.168.1.100 set -swing-v -eco -no-beep
  midea -device 192.168.1.100 setprop -swing-v-angle 50
  midea -device 192.168.1.100 capabilities
  midea -device 192.168.1.100 energy`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
