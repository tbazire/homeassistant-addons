package internal

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	shipapi "github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
)

// Config holds all runtime configuration parsed from command-line flags.
type Config struct {
	// Local service
	Port       uint
	CertPath   string
	KeyPath    string
	Brand      string
	Model      string
	Serial     string
	VendorCode string
	AutoAccept bool
	Heartbeat  time.Duration

	// Pairing targets / discovery
	RemoteSKI string
	SecretHex string
	ListAll   bool // list discovered services and exit if none chosen

	// Output / logging
	LogLevel string
	JSONOut  bool // emit measurement data as JSON lines

	// Data refresh
	// PollInterval caps how often the scanner proactively re-issues SPINE read
	// requests (RequestData/RequestDescriptions/...) per remote entity. Values
	// pushed by the device via subscriptions are still displayed immediately;
	// only our主动 pull is throttled. This prevents the amplification loop
	// RequestData -> DataChange -> RequestEntityData -> RequestData.
	// Exposed as a flag now and as an options.json key later (HA add-on).
	PollInterval time.Duration

	// Cert/key persistence
	CertDir string
}

// RegisterFlags wires every flag into the provided FlagSet. Pass flag.CommandLine
// for the default CLI or a custom FlagSet for testing.
func (c *Config) RegisterFlags(fs *flag.FlagSet) {
	fs.UintVar(&c.Port, "port", 4711, "local SHIP websocket port to listen on")
	fs.StringVar(&c.CertPath, "certpath", "", "PEM certificate path (auto-generated if empty)")
	fs.StringVar(&c.KeyPath, "keypath", "", "PEM private key path (auto-generated if empty)")
	fs.StringVar(&c.Brand, "brand", "EEBusScanner", "local device brand")
	fs.StringVar(&c.Model, "model", "Scanner-1", "local device model")
	fs.StringVar(&c.Serial, "serial", "scanner-0001", "local device serial number")
	fs.StringVar(&c.VendorCode, "vendor", "SCNR", "local vendor code (3+ chars)")
	fs.BoolVar(&c.AutoAccept, "autoaccept", false, "auto-trust any incoming pairing request (insecure, demo only)")
	fs.DurationVar(&c.Heartbeat, "heartbeat", 4*time.Second, "SHIP heartbeat timeout")

	fs.StringVar(&c.RemoteSKI, "remoteski", "", "SKI (40 hex chars) of the remote service to pair with")
	fs.StringVar(&c.SecretHex, "secret", "", "SHIP Pairing secret (hex), enables PairingModeListener")
	fs.BoolVar(&c.ListAll, "list", false, "list discovered services on the network and exit")

	fs.StringVar(&c.LogLevel, "loglevel", "info", "log level: trace|debug|info|error")
	fs.BoolVar(&c.JSONOut, "json", false, "emit measurement data as JSON lines (machine-friendly)")

	// PollInterval caps the proactive re-read cadence per remote entity.
	// Notifications pushed by the device are always handled immediately; only
	// our own RequestData pulls are throttled to this interval. Set to 0 to
	// disable the proactive pull entirely (subscription-only mode).
	fs.DurationVar(&c.PollInterval, "poll-interval", 60*time.Second,
		"minimum interval between proactive SPINE data pulls per entity (0 = subscription-only)")

	fs.StringVar(&c.CertDir, "certdir", "./certs", "directory used to persist auto-generated cert/key and ring buffer")
}

// Validate checks the configuration consistency. Returns a descriptive error
// for misuse (empty required fields, malformed SKI or secret, etc.).
func (c *Config) Validate() error {
	if c.Port == 0 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if len(c.VendorCode) < 1 {
		return errors.New("vendor code must not be empty")
	}
	if c.RemoteSKI != "" {
		if len(c.RemoteSKI) != 40 {
			return fmt.Errorf("remoteski must be exactly 40 hex chars, got %d", len(c.RemoteSKI))
		}
		if _, err := hex.DecodeString(c.RemoteSKI); err != nil {
			return fmt.Errorf("remoteski is not valid hex: %w", err)
		}
	}
	if c.SecretHex != "" {
		b, err := hex.DecodeString(c.SecretHex)
		if err != nil {
			return fmt.Errorf("secret is not valid hex: %w", err)
		}
		// SHIP Pairing secret is 16 bytes (see ship-go PairingSecret.IsValidLength).
		if len(b) != 16 {
			return fmt.Errorf("secret must decode to 16 bytes, got %d", len(b))
		}
	}
	// certpath/keypath must be both set or both empty.
	if (c.CertPath == "") != (c.KeyPath == "") {
		return errors.New("certpath and keypath must be both set or both empty")
	}
	return nil
}

// String produces a human-readable dump of the active configuration
// (secrets are not printed).
func (c *Config) String() string {
	secret := "<none>"
	if c.SecretHex != "" {
		secret = "<set," + fmt.Sprintf("%d", len(c.SecretHex)/2) + "B>"
	}
	return fmt.Sprintf(
		"port=%d brand=%s model=%s serial=%s vendor=%s\n"+
			"certpath=%s keypath=%s certdir=%s\n"+
			"remoteski=%s secret=%s autoaccept=%v heartbeat=%s\n"+
			"loglevel=%s json=%v list=%v poll-interval=%s",
		c.Port, c.Brand, c.Model, c.Serial, c.VendorCode,
		c.CertPath, c.KeyPath, c.CertDir,
		c.RemoteSKI, secret, c.AutoAccept, c.Heartbeat,
		c.LogLevel, c.JSONOut, c.ListAll, c.PollInterval,
	)
}

// LoadOrGenerateCertificate returns the local TLS certificate to use for the
// SHIP handshake. Behavior:
//   - If certpath/keypath are set, load them.
//   - Otherwise auto-generate an EC P-256 cert (via ship-go/cert) and persist
//     it under certdir so the local SKI stays stable across restarts. Losing
//     the SKI would force re-pairing with every remote.
//
// Returns the certificate plus the path it was loaded/saved from (for logging).
func (c *Config) LoadOrGenerateCertificate() (tls.Certificate, string, string, error) {
	if c.CertPath != "" && c.KeyPath != "" {
		cert, err := tls.LoadX509KeyPair(c.CertPath, c.KeyPath)
		if err != nil {
			return tls.Certificate{}, "", "", fmt.Errorf("load cert/key: %w", err)
		}
		return cert, c.CertPath, c.KeyPath, nil
	}

	// Auto-generate and persist.
	if err := os.MkdirAll(c.CertDir, 0o700); err != nil {
		return tls.Certificate{}, "", "", fmt.Errorf("mkdir certdir: %w", err)
	}
	certPath := filepath.Join(c.CertDir, "scanner.crt")
	keyPath := filepath.Join(c.CertDir, "scanner.key")

	// Reuse previously generated cert if present.
	if fileExists(certPath) && fileExists(keyPath) {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err == nil {
			return cert, certPath, keyPath, nil
		}
		AppLog.Warnf("existing cert/key invalid (%v), regenerating", err)
	}

	AppLog.Infof("generating new EC P-256 certificate -> %s / %s", certPath, keyPath)
	certificate, err := cert.CreateCertificate(c.Brand, c.Model, "FR", c.Serial)
	if err != nil {
		return tls.Certificate{}, "", "", fmt.Errorf("create certificate: %w", err)
	}
	if err := saveCertificate(certificate, certPath, keyPath); err != nil {
		return tls.Certificate{}, "", "", fmt.Errorf("save certificate: %w", err)
	}
	return certificate, certPath, keyPath, nil
}

// BuildPairingConfig translates the -secret flag into a shipapi.PairingConfig.
// Returns nil when no secret is set (classic SKI-based pairing applies).
// When non-nil, also returns the FileRingBuffer that must back it.
func (c *Config) BuildPairingConfig() (*shipapi.PairingConfig, error) {
	if c.SecretHex == "" {
		return nil, nil
	}
	secretBytes, err := hex.DecodeString(c.SecretHex)
	if err != nil {
		return nil, fmt.Errorf("decode secret: %w", err)
	}
	pc := shipapi.NewPairingConfig(shipapi.PairingModeListener, shipapi.PairingSecret(secretBytes))
	return pc, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func saveCertificate(certificate tls.Certificate, certPath, keyPath string) error {
	// Serialize certificate chain.
	var pemOut []byte
	for _, der := range certificate.Certificate {
		pemOut = append(pemOut, pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: der,
		})...)
	}
	if err := os.WriteFile(certPath, pemOut, 0o600); err != nil {
		return err
	}
	// Serialize EC private key.
	keyDER, err := x509.MarshalECPrivateKey(certificate.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(keyPath, keyPEM, 0o600)
}
