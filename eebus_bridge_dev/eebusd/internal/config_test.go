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
