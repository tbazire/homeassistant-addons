// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire

package internal

import "testing"

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
