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

// TestLoad_MQTTParsing asserts every EEBUS_MQTT_* env var (exported by run.sh,
// either from an external broker config or from Supervisor discovery) lands in
// the right Config field, including the TLS toggle added for external brokers.
func TestLoad_MQTTParsing(t *testing.T) {
	set := func(k, v string) {
		t.Cleanup(func() { os.Unsetenv(k) })
		os.Setenv(k, v)
	}
	set("EEBUS_MQTT_HOST", "broker.example.com")
	set("EEBUS_MQTT_PORT", "8883")
	set("EEBUS_MQTT_USER", "eebus")
	set("EEBUS_MQTT_PASSWORD", "secret")
	set("EEBUS_MQTT_SSL", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MQTTHost != "broker.example.com" {
		t.Errorf("MQTTHost = %q", cfg.MQTTHost)
	}
	if cfg.MQTTPort != 8883 {
		t.Errorf("MQTTPort = %d, want 8883", cfg.MQTTPort)
	}
	if cfg.MQTTUser != "eebus" {
		t.Errorf("MQTTUser = %q", cfg.MQTTUser)
	}
	if cfg.MQTTPassword != "secret" {
		t.Errorf("MQTTPassword = %q", cfg.MQTTPassword)
	}
	if !cfg.MQTTTLS {
		t.Error("MQTTTLS = false, want true")
	}
}

// TestLoad_MQTTHostRequired: without a broker the bridge has nothing to
// publish to — Load must refuse to build so s6 restarts with a clear error
// instead of running a silently useless daemon.
func TestLoad_MQTTHostRequired(t *testing.T) {
	t.Setenv("EEBUS_MQTT_HOST", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load succeeded without EEBUS_MQTT_HOST, want error")
	}
}

// TestBrokerURL: TLS flips the paho scheme from tcp:// to ssl:// (external
// brokers on 8883 / cloud brokers). Without TLS nothing changes.
func TestBrokerURL(t *testing.T) {
	opts := MQTTOptions{Host: "broker.example.com", Port: 1883}
	if got := brokerURL(opts); got != "tcp://broker.example.com:1883" {
		t.Errorf("plain = %q", got)
	}
	opts.TLS = true
	opts.Port = 8883
	if got := brokerURL(opts); got != "ssl://broker.example.com:8883" {
		t.Errorf("tls = %q", got)
	}
}
