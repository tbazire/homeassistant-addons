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
	"bufio"
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

	// The SHIP/SPINE logger (wired into service.SetLogging) writes raw transport
	// frames via logging.Log().Trace("Send:"/"Recv:", ski, ...). NewLogger
	// defaults to os.Stdout, which in -json mode would interleave with the
	// NDJSON stream and break the bridge parser. Redirect it to the same
	// destination as the other loggers so stdout stays data-only.
	logger := internal.NewLogger(logLevel)
	logger.SetWriter(logWriter)

	app := internal.NewApp(cfg, logger)
	if err := app.Setup(); err != nil {
		internal.AppLog.Errorf("setup failed: %v", err)
		os.Exit(1)
	}

	if err := app.Start(); err != nil {
		internal.AppLog.Errorf("start failed: %v", err)
		os.Exit(1)
	}

	// If write commands are enabled, start a goroutine reading NDJSON command
	// lines from stdin (one JSON object per line). The bridge writes commands
	// there to drive the write use cases (OHPCF, LPC, …). Each line is routed
	// to the dispatcher; the result is emitted on stdout as a command_result
	// line. The goroutine exits when stdin is closed (bridge shutdown).
	if app.WritesEnabled() {
		go readCommands(app)
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

// readCommands reads NDJSON command lines from stdin and forwards each to
// app.HandleCommand. Errors are logged but never abort the reader: the bridge
// may emit a malformed line on shutdown and the stream must keep working for
// the next command.
func readCommands(app *internal.App) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		// The dispatcher wants its own copy: the underlying buffer is reused
		// across Scan calls, and Dispatch may outlive the next iteration when
		// the SPINE result is asynchronous.
		dup := make([]byte, len(line))
		copy(dup, line)
		if err := app.HandleCommand(dup); err != nil {
			internal.AppLog.Warnf("command dispatch: %v", err)
		}
	}
	if err := sc.Err(); err != nil {
		internal.AppLog.Infof("stdin command reader stopped: %v", err)
	} else {
		internal.AppLog.Infof("stdin command reader: EOF (bridge closed the pipe)")
	}
}
