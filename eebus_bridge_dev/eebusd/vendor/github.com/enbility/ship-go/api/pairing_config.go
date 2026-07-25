package api

import "time"

// PairingConfig defines pairing behavior
type PairingConfig struct {
	// Core configuration (required)
	Mode   PairingMode   // Operating mode: Off, Listener, Announcer, Both
	Secret PairingSecret // 16-byte shared secret for HMAC validation (from QR code SPSEC field)

	// AnnouncementLifetimeTimeout defines how long a continuous, uninterrupted
	// SHIP connection must be maintained before the corresponding announcement
	// is removed. Defaults to 15 minutes per spec if zero.
	AnnouncementLifetimeTimeout time.Duration
}

// NewPairingConfig creates a new pairing configuration with the specified mode and secret
func NewPairingConfig(mode PairingMode, secret PairingSecret) *PairingConfig {
	return &PairingConfig{
		Mode:   mode,
		Secret: secret,
	}
}

// Validate validates the pairing configuration
func (c *PairingConfig) Validate() error {
	if c == nil {
		return nil // nil config is valid (no pairing)
	}

	// Validate secret length for HMAC operations
	if c.Mode != PairingModeOff && len(c.Secret) > 0 {
		if !c.Secret.IsValidLength() {
			return ErrInvalidSecret
		}
	}

	return nil
}
