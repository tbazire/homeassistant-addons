// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 Tommy Bazire
//
// Package internal: config_test.go — regression coverage for Config.Args().
//
// The critical regression: Go's flag package does NOT accept a space-separated
// value for a bool flag ("-flag value"); it treats the value as a positional
// argument, which stops flag parsing. The two write enable flags are bools, so
// they MUST be emitted as a single "-flag=value" token. Otherwise only the
// first one is ever seen by the daemon (the second is swallowed as a positional
// arg), and OHPCF silently never activates — exactly the 0.6.7 bug.

package internal

import (
	"os"
	"strings"
	"testing"
)

// TestArgs_BoolFlagsUseEqualsForm is the direct regression test for the
// space-separated-bool bug. It asserts that every bool flag in Args() is
// emitted as "-name=value" (one slice element), never "-name" followed by a
// separate "value".
func TestArgs_BoolFlagsUseEqualsForm(t *testing.T) {
	withWriteEnv(t, true, true)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	args := cfg.Args()

	// Every bool write flag must appear as a single "-flag=value" token.
	for _, flag := range []string{"-write-lpc-enabled", "-write-ohpcf-enabled"} {
		for _, a := range args {
			if a == flag {
				t.Errorf("bool flag %q emitted as a bare token: a space-separated "+
					"value stops Go flag parsing. Use %s=true instead.\nargs=%v",
					flag, flag, args)
			}
			if strings.HasPrefix(a, flag+"=") {
				goto found
			}
		}
		t.Errorf("bool flag %q missing from Args() entirely\nargs=%v", flag, args)
	found:
	}
}

// TestArgs_BothWriteFlagsPropagate asserts that the two write enable toggles
// survive into Args() with the correct boolean value, including the OHPCF flag
// (which the 0.6.7 bug silently dropped).
func TestArgs_BothWriteFlagsPropagate(t *testing.T) {
	cases := []struct {
		name               string
		lpc, ohpcf         bool
		wantLPC, wantOHPCF string
	}{
		{"both true", true, true, "true", "true"},
		{"both false", false, false, "false", "false"},
		{"lpc only", true, false, "true", "false"},
		{"ohpcf only", false, true, "false", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withWriteEnv(t, tc.lpc, tc.ohpcf)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			args := cfg.Args()
			assertFlagValue(t, args, "-write-lpc-enabled", tc.wantLPC)
			assertFlagValue(t, args, "-write-ohpcf-enabled", tc.wantOHPCF)
		})
	}
}

// assertFlagValue finds "-flag=value" in args and checks value.
func assertFlagValue(t *testing.T, args []string, flag, want string) {
	t.Helper()
	prefix := flag + "="
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			got := strings.TrimPrefix(a, prefix)
			if got != want {
				t.Errorf("%s: got %q, want %q\nargs=%v", flag, got, want, args)
			}
			return
		}
	}
	t.Errorf("%s not found in args=%v", flag, args)
}

// withWriteEnv sets the env vars the run script exports, then cleans up.
func withWriteEnv(t *testing.T, lpc, ohpcf bool) {
	t.Helper()
	set := func(k, v string) {
		t.Cleanup(func() { os.Unsetenv(k) })
		os.Setenv(k, v)
	}
	set("EEBUS_MQTT_HOST", "127.0.0.1")
	set("EEBUS_WRITE_ENABLE", "true")
	set("EEBUS_WRITE_LPC_ENABLED", boolStr(lpc))
	set("EEBUS_WRITE_OHPCF_ENABLED", boolStr(ohpcf))
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
