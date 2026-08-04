// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package internal

import (
	"flag"
	"io"
	"testing"
)

// TestEffectiveLPCMaxLimit pins the fallback resolution for the LPC number
// max: a configured positive value wins; 0 (or negative) falls back to
// DefaultLPCMaxLimitW. This is the rule that prevents the HA slider from
// being silently capped at 100 W when the device exposes no nominal_max.
func TestEffectiveLPCMaxLimit(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"zero falls back to default", 0, DefaultLPCMaxLimitW},
		{"negative falls back to default", -5, DefaultLPCMaxLimitW},
		{"positive config wins", 12000, 12000},
		{"large config wins", 50000, 50000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{LPCMaxLimitW: c.in}
			got := cfg.EffectiveLPCMaxLimit()
			if got != c.want {
				t.Errorf("EffectiveLPCMaxLimit() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestDefaultLPCMaxLimitWIsResidential asserts the hard-coded default is a
// sane residential ceiling (>= typical 3-phase wallbox 22 kW). Changing it
// silently would either cap legitimate limits or advertise a nonsensical max.
func TestDefaultLPCMaxLimitWIsResidential(t *testing.T) {
	if DefaultLPCMaxLimitW < 22000 {
		t.Errorf("DefaultLPCMaxLimitW = %v, want >= 22000 (residential ceiling)", DefaultLPCMaxLimitW)
	}
	if DefaultLPCMaxLimitW > 100000 {
		t.Errorf("DefaultLPCMaxLimitW = %v, want <= 100000 (sanity upper bound)", DefaultLPCMaxLimitW)
	}
}

// TestUseCaseEnabled pins the per-use-case security gate. This is the single
// predicate BindAll and the daemon loops consult; getting it wrong would mean
// either (a) a use case binds despite the user leaving it off — a phantom HA
// entity and an unwanted command surface — or (b) a use case the user enabled
// silently stays inert. The fail-closed default for unknown names is the most
// security-relevant case: a future use case shipped before its toggle is wired
// must NOT become active by accident.
func TestUseCaseEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		uc   string
		want bool
	}{
		{
			name: "lpc off when flag false",
			cfg:  &Config{LPCEnabled: false, OHPCFEnabled: true},
			uc:   "lpc",
			want: false,
		},
		{
			name: "lpc on when flag true",
			cfg:  &Config{LPCEnabled: true, OHPCFEnabled: false},
			uc:   "lpc",
			want: true,
		},
		{
			name: "ohpcf off when flag false",
			cfg:  &Config{LPCEnabled: true, OHPCFEnabled: false},
			uc:   "ohpcf",
			want: false,
		},
		{
			name: "ohpcf on when flag true",
			cfg:  &Config{LPCEnabled: false, OHPCFEnabled: true},
			uc:   "ohpcf",
			want: true,
		},
		{
			name: "unknown use case fails closed (all off)",
			cfg:  &Config{LPCEnabled: false, OHPCFEnabled: false},
			uc:   "future_lpp",
			want: false,
		},
		{
			name: "unknown use case fails closed even when both known are on",
			cfg:  &Config{LPCEnabled: true, OHPCFEnabled: true},
			uc:   "future_opev",
			want: false,
		},
		{
			name: "empty name fails closed",
			cfg:  &Config{LPCEnabled: true, OHPCFEnabled: true},
			uc:   "",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.UseCaseEnabled(c.uc); got != c.want {
				t.Errorf("UseCaseEnabled(%q) = %v, want %v", c.uc, got, c.want)
			}
		})
	}
}

// TestParseBridgeArgs_BoolFlagsResolve is the regression test for the 0.6.7
// bug where OHPCF buttons never appeared in Home Assistant. The root cause:
// the bridge emitted the per-use-case bool flags as "-write-lpc-enabled true"
// (space-separated), but Go's flag package does NOT consume a separate value
// for a bool flag — it sets the flag from the bare token and treats the next
// arg ("true") as a positional, which stops flag.Parse. As a result the
// second bool flag (-write-ohpcf-enabled) was never parsed and OHPCF never
// bound. This test parses the bridge's exact Args() shape with a real
// flag.FlagSet and asserts BOTH flags resolve.
func TestParseBridgeArgs_BoolFlagsResolve(t *testing.T) {
	// Exact shape produced by bridge Config.Args() (see bridge/internal/config.go):
	// bool flags use the "-flag=value" single-token form.
	bridgeArgs := []string{
		"-json", "-loglevel", "info", "-poll-interval", "60s",
		"-brand", "EEBusBridge", "-model", "Bridge-1",
		"-serial", "bridge-0001", "-vendor", "EBRG",
		"-port", "4711", "-certdir", "/data/eebus",
		"-commands", "-write-usecases", "auto", "-write-profile", "auto",
		"-write-lpc-max-limit-w", "0",
		"-write-lpc-enabled=true",
		"-write-ohpcf-enabled=true",
	}
	cfg := parseOrFail(t, bridgeArgs)
	if !cfg.Commands {
		t.Errorf("Commands: got false, want true")
	}
	if !cfg.LPCEnabled {
		t.Errorf("LPCEnabled: got false, want true (0.6.7 regression: first bool " +
			"was set but parsing stopped here)")
	}
	if !cfg.OHPCFEnabled {
		t.Errorf("OHPCFEnabled: got false, want true — THIS IS THE 0.6.7 BUG: " +
			"the second bool flag was swallowed as a positional and never parsed")
	}
	// No leftover positional args: that would indicate a flag stopped parsing.
	raw := parseOrFailRaw(t, bridgeArgs)
	if extra := raw.flagSet.Args(); len(extra) > 0 {
		t.Errorf("unexpected positional args after parse (flag parsing stopped): %v", extra)
	}
}

// TestParseBridgeArgs_SpaceSeparatedBoolFails documents the buggy shape so the
// regression cannot silently come back. It pins the broken form and shows the
// symptom (OHPCF swallowed as positional). This is intentionally asserting the
// BROKEN behavior of the buggy form, to make the gotcha visible forever.
func TestParseBridgeArgs_SpaceSeparatedBoolFails(t *testing.T) {
	// Buggy shape (pre-fix): space-separated bool values.
	buggy := []string{
		"-write-lpc-enabled", "true",
		"-write-ohpcf-enabled", "true",
	}
	raw := parseOrFailRaw(t, buggy)
	// LPC parses (bare bool flag = true), but its "true" value becomes the
	// first positional, so OHPCF is never reached.
	if !raw.cfg.LPCEnabled {
		t.Errorf("LPC should still be true (bare flag), got false")
	}
	if raw.cfg.OHPCFEnabled {
		t.Errorf("OHPCF should be false (the buggy form), got true — if this is " +
			"true the flag package changed its bool semantics, revisit the fix")
	}
	if len(raw.flagSet.Args()) == 0 {
		t.Errorf("expected leftover positional args (symptom of the bug), got none")
	}
}

// rawParse holds the parsed config and the flagset (for Args() inspection).
type rawParse struct {
	cfg     *Config
	flagSet *flag.FlagSet
}

func parseOrFail(t *testing.T, args []string) *Config {
	t.Helper()
	return parseOrFailRaw(t, args).cfg
}

func parseOrFailRaw(t *testing.T, args []string) rawParse {
	t.Helper()
	cfg := &Config{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // silence usage on error
	cfg.RegisterFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("flag parse: %v\nargs=%v", err, args)
	}
	return rawParse{cfg: cfg, flagSet: fs}
}
