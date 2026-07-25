// Command eebusd is the EEBUS daemon: it advertises itself as a CEM over mDNS,
// pairs with a chosen remote EEBUS service (by SKI), and then scans and emits
// all the data the remote exposes — manufacturer details, device configuration,
// measurements (power/energy/current/voltage/...), diagnosis state, plus typed
// use case data (MGCP/MPC/VABD/VAPD).
//
// Output modes:
//   - default: human-readable tables on stdout
//   - -json   : one JSON object per line (NDJSON) on stdout, logs on stderr
//
// eebusd is the EEBUS-pure half of the eebus_bridge add-on; the MQTT/HA bridge
// is a separate binary (eebus-bridge) that consumes this NDJSON stream.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"eebusd/internal"
	"eebusd/internal/scanner"
)

func main() {
	cfg := &internal.Config{}
	cfg.RegisterFlags(flag.CommandLine)
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration: %v\n\n", err)
		flag.Usage()
		os.Exit(2)
	}

	// Initialize the global loggers early so setup messages are visible.
	//
	// In -json mode the data stream goes to stdout (one JSON object per line,
	// consumed by eebus-bridge), so logs MUST go to stderr to avoid interleaving
	// and corrupting the stream. In the default human-readable mode, logs and
	// tables both go to stdout as before.
	logLevel := internal.ParseLogLevel(cfg.LogLevel)
	logWriter := os.Stdout
	if cfg.JSONOut {
		logWriter = os.Stderr
	}
	internal.InitAppLog(logLevel, logWriter)
	scanner.SetLogLevel(cfg.LogLevel)
	scanner.SetLogWriter(logWriter)

	internal.AppLog.Infof("eebus-scanner starting")
	internal.AppLog.Infof("configuration:\n%s", cfg.String())

	logger := internal.NewLogger(logLevel)

	app := internal.NewApp(cfg, logger)
	if err := app.Setup(); err != nil {
		internal.AppLog.Errorf("setup failed: %v", err)
		os.Exit(1)
	}

	if err := app.Start(); err != nil {
		internal.AppLog.Errorf("start failed: %v", err)
		os.Exit(1)
	}

	// Wait for SIGINT / SIGTERM, then shut down cleanly. mDNS teardown must
	// happen or the service keeps announcing on the network.
	internal.AppLog.Infof("running — press Ctrl+C to stop")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	internal.AppLog.Infof("shutting down...")
	app.Shutdown()
}
