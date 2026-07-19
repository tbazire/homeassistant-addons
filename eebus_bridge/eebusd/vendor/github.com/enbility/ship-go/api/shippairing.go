package api

import (
	"context"
	"time"
)

/* SHIP Pairing Service Core Interfaces */

// ShipPairingServiceInterface - Main interface for SHIP pairing service functionality
// Separates lifecycle from operations for better testability
type ShipPairingServiceInterface interface {
	// Start initializes and starts the SHIP pairing service.
	// Must be called before any pairing operations can be performed.
	// Returns an error if the service cannot be started (e.g., missing configuration).
	Start() error

	// Shutdown gracefully stops the pairing service and cleans up resources.
	// Should be called when the service is no longer needed to ensure proper cleanup.
	Shutdown()

	// CreateListener creates a new pairing listener instance for the specified local service.
	// Used when this device acts as devA (target device) in SHIP pairing.
	// Returns a configured listener ready to accept pairing requests.
	CreateListener() PairingListenerInterface

	// CreateAnnouncer creates a new pairing announcer instance for the specified local service.
	// Used when this device acts as devZ (requesting device) in SHIP pairing.
	// Returns a configured announcer ready to send pairing requests.
	CreateAnnouncer() PairingAnnouncerInterface

	// IsServiceRunning returns whether the PairingService was started successfully and is currently running
	IsServiceRunning() bool
}

// PairingAnnouncerInterface - Interface for devZ (announcer) operations
type PairingAnnouncerInterface interface {
	// Announce sends a pairing request to the specified target device via mDNS.
	// This method implements the devZ (requesting device) role in SHIP pairing.
	// The announcement includes HMAC-signed pairing data and runs until pairing
	// succeeds, fails, or is cancelled.
	//
	// Parameters:
	//   - target: The target device information including SKI, fingerprint, and pairing secret
	//
	// Returns:
	//   - nil if announcement starts successfully (pairing may still be in progress)
	//   - error if announcement cannot be started or target is invalid
	Announce(target PairingTarget) error

	// StopAnnouncement cancels any active pairing announcement.
	// This method safely stops the announcement process and cleans up resources.
	// Returns an error if no announcement is active or stopping fails.
	StopAnnouncement() error

	// GetAnnouncementStatus returns the current status of any active announcement.
	// Provides information about announcement progress, attempts, and any errors.
	GetAnnouncementStatus() AnnouncementStatus
}

// PairingListenerInterface - Interface for devA (listener) operations
type PairingListenerInterface interface {
	// StartListening begins listening for SHIP pairing requests via mDNS.
	// This method implements the devA (target device) role in SHIP pairing.
	// The listener validates incoming pairing requests against the provided secret
	// and automatically accepts valid requests.
	//
	// Parameters:
	//   - ctx: Context for cancellation control (listener stops when context is cancelled)
	//   - secret: The pairing secret used to validate incoming HMAC signatures
	//
	// Returns:
	//   - nil if listening starts successfully
	//   - error if listening cannot be started or secret is invalid
	//
	// Behavior:
	//   - Runs until context is cancelled, pairing succeeds, or an error occurs
	//   - Automatically validates HMAC signatures from announcing devices
	//   - Triggers pairing success/failure callbacks via PairingServiceReaderInterface
	StartListening(ctx context.Context, secret PairingSecret) error

	// StopListening cancels any active pairing listener.
	// This method safely stops the listening process and cleans up resources.
	// Returns an error if no listener is active or stopping fails.
	StopListening() error

	// GetListenerStatus returns the current status of the pairing listener.
	// Provides information about listening activity, requests seen, and any errors.
	GetListenerStatus() ListenerStatus

	// ProcessPendingEntries processes a batch of pairing entries that were found
	// but not yet processed. This is typically used when reactivating after
	// device replacement timeouts to handle existing mDNS announcements.
	//
	// Parameters:
	//   - entries: Map of service names to ShipPairingTXT records to process
	//
	// Returns:
	//   - error if processing fails (nil for successful processing)
	//
	// Behavior:
	//   - Processes each entry through the same validation pipeline as live discovery
	//   - Stops processing after first successful pairing (SHIP spec behavior)
	//   - Handles invalid entries gracefully, continuing with remaining entries
	//   - No-op if listener is not active or entries is nil/empty
	ProcessPendingEntries(entries map[string]*ShipPairingTXT) error
}

/* Integration Interfaces */

// PairingHubInterface - Interface for Hub integration
// Implemented by Hub, used by PairingService
type PairingHubInterface interface {
	// OnPairingSuccess is called when SHIP pairing completes successfully.
	// The hub should add the device to its trust store and enable connections.
	// This callback indicates that HMAC validation passed and the device should
	// be automatically trusted for SHIP connections.
	//
	// Parameters:
	//   - remoteShipID: The SHIP ID of the successfully paired device
	//   - remoteFingerprint: SHA-256 fingerprint of the device's certificate
	OnPairingSuccess(remoteShipID, remoteFingerprint string)

	// OnPairingFailure is called when SHIP pairing fails.
	// The hub should not trust the device and may log the failure for security monitoring.
	// Common failure reasons include invalid HMAC, replay attacks, or network errors.
	//
	// Parameters:
	//   - remoteShipID: The SHIP ID of the device that failed pairing
	//   - remoteFingerprint: SHA-256 fingerprint of the device's certificate
	//   - reason: The specific error that caused the pairing failure
	OnPairingFailure(remoteShipID, remoteFingerprint string, reason error)

	// GetTrustedAddCuDevice returns the ServiceDetails of any trusted AddCu device, or nil if none.
	// This is used by the pairing listener to check if the trust slot is occupied
	// before accepting new AddCu pairing requests.
	//
	// Returns:
	//   - ServiceDetails : The ServiceDetails of the AddCu trusted device, or nil if none.
	GetTrustedAddCuDevice() *ServiceDetails
}

// PairingCryptoInterface - Interface for HMAC operations
// Implemented by crypto manager, used by PairingService
type PairingCryptoInterface interface {
	// GenerateNonce creates a cryptographically secure 128-bit nonce.
	// The nonce is used in HMAC calculation to prevent replay attacks
	// and ensure each pairing request is unique per SHIP specification.
	//
	// Returns:
	//   - 16-byte (128-bit) cryptographically random nonce
	//   - error if secure random number generation fails
	//
	// Security: Must use a cryptographically secure random number generator
	GenerateNonce() ([]byte, error)

	// CalculateDigest computes HMAC-SHA256 digest for SHIP pairing validation.
	// Implements the HMAC calculation specified in SHIP Pairing Service section 7.
	// The digest is used to prove knowledge of the shared pairing secret.
	//
	// Parameters:
	//   - secret: The shared pairing secret (from QR code SPSEC field)
	//   - params: HMAC parameters including algorithm, nonce, and TXT record data
	//
	// Returns:
	//   - 32-byte HMAC-SHA256 digest
	//   - error if HMAC calculation fails
	//
	// Security: Uses HMAC-SHA256 with proper message construction per SHIP spec
	CalculateDigest(secret PairingSecret, params HMACParams) ([]byte, error)

	// ValidateDigest verifies an HMAC digest using constant-time comparison.
	// Prevents timing attacks by ensuring validation always takes the same time
	// regardless of where the digests differ.
	//
	// Parameters:
	//   - secret: The shared pairing secret used for validation
	//   - params: HMAC parameters used to recalculate the expected digest
	//   - expectedDigest: The digest to validate against
	//
	// Returns:
	//   - nil if digests match (validation successful)
	//   - error if digests don't match or calculation fails
	//
	// Security: Uses constant-time comparison to prevent timing attacks
	ValidateDigest(secret PairingSecret, params HMACParams, expectedDigest []byte) error
}

// RingBufferPersistence - Application-implemented storage interface for SHIP pairing ring buffer
// Applications implement this to provide persistent storage for the ring buffer state per SHIP
// Pairing Service specification section 11. The library handles all ring buffer logic internally.
//
// Thread Safety: Implementations must be thread-safe as methods may be called concurrently.
// Storage Requirements: Data must persist across application restarts per SHIP specification.
type RingBufferPersistence interface {
	// LoadRingBuffer restores the ring buffer state from persistent storage.
	// Called once during Hub initialization to restore previous state.
	//
	// Returns:
	//   - entries: Array of DigestEntry (may be larger than used entries)
	//   - nextIndex: Index where next entry will be written (0 to len(entries)-1)
	//   - error: Any error loading from storage
	//
	// For new installations or empty storage:
	//   - Return empty slice and nextIndex=0
	//   - Do not return an error for "no data found" cases
	//
	// Error cases that should return errors:
	//   - Storage system unavailable
	//   - Corrupted data that cannot be parsed
	//   - Permission denied accessing storage
	//
	// Example implementation patterns:
	//   File-based: Load JSON/GZIP from file
	//   Database: SELECT entries, next_index FROM ring_buffer WHERE device_id = ?
	//   Memory: Return in-memory state (for testing only)
	LoadRingBuffer() ([]DigestEntry, int, error)

	// SaveRingBuffer persists the current ring buffer state to storage.
	// Called after each successful pairing to ensure replay protection is maintained.
	//
	// Parameters:
	//   - entries: Complete ring buffer array (may contain unused entries with empty Algorithm)
	//   - nextIndex: Index where next entry will be written (0 to len(entries)-1)
	//
	// Implementation requirements:
	//   - Must be atomic (either fully saves or fails completely)
	//   - Should handle concurrent calls gracefully
	//   - Must maintain consistency even if application crashes during save
	//
	// Error handling:
	//   - Return error if save fails for any reason
	//   - Do not partially update storage
	//   - Library will log errors but continue operation
	//
	// Example implementation patterns:
	//   File-based: Write to temp file, then atomic rename
	//   Database: Use transactions for atomic updates
	//   Memory: Update in-memory state (for testing only)
	SaveRingBuffer(entries []DigestEntry, nextIndex int) error
}

// PairingServiceReaderInterface - Callback interface for pairing events
// Applications implement this to receive pairing event notifications
type PairingServiceReaderInterface interface {
	// Called when service is automatically trusted via SHIP pairing
	// The identity parameter contains all necessary device identification information
	ServiceAutoTrusted(identity ServiceIdentity)

	// Called when SHIP pairing fails for a service
	// The identity parameter contains device identification, reason explains the failure
	ServiceAutoTrustFailed(identity ServiceIdentity, reason error)

	// ServiceAutoTrustRemoved is called when device trust is automatically
	// removed as part of the Device Replacement Timing Logic feature.
	//
	// This callback is triggered in two scenarios:
	//
	// 1. **15-minute timeout expired**: When an AddCu device disconnects and doesn't
	//    reconnect within 15 minutes, the system assumes it has been replaced.
	//    The trust is removed and the pairing listener is reactivated.
	//    Reason format: "AddCu replacement timeout for device <shipID>"
	//
	// 2. **New pairing replaces existing trust**: When a new device pairs using the
	//    same pairing credentials, it automatically replaces the trust of the
	//    previously paired device.
	//    Reason format: "Replaced by new device pairing from <shipID>"
	//
	// Parameters:
	// - identity: The ServiceIdentity of the device whose trust was removed
	// - reason: Human-readable explanation of why trust was removed
	//
	// Example implementation:
	//
	//	func (r *MyReader) ServiceAutoTrustRemoved(identity api.ServiceIdentity, reason string) {
	//	    log.Printf("Device trust removed: SKI=%s, ShipID=%s, Reason=%s",
	//	        identity.SKI, identity.ShipID, reason)
	//
	//	    // Update UI to show device is no longer trusted
	//	    updateDeviceStatus(identity.ShipID, "untrusted")
	//
	//	    // Clean up any device-specific resources
	//	    cleanupDeviceResources(identity.SKI)
	//
	//	    // Optionally notify user
	//	    if strings.Contains(reason, "timeout") {
	//	        notifyUser("Device %s disconnected and timed out", identity.ShipID)
	//	    } else if strings.Contains(reason, "Replaced") {
	//	        notifyUser("Device %s was replaced by a new device", identity.ShipID)
	//	    }
	//	}
	ServiceAutoTrustRemoved(identity ServiceIdentity, reason string)
}

/* Security Types */

// PairingSecret represents a secure pairing secret with explicit clearing
type PairingSecret []byte

// Clear securely overwrites the secret in memory with zeros.
// This method should be called when the secret is no longer needed to prevent
// sensitive data from remaining in memory. Uses explicit zeroing to ensure
// the compiler doesn't optimize away the operation.
//
// Example usage:
//
//	defer secret.Clear()
func (s PairingSecret) Clear() {
	for i := range s {
		s[i] = 0
	}
}

// String returns "[REDACTED]" to prevent accidental exposure of sensitive data.
// This ensures that if a PairingSecret is accidentally included in log messages,
// error strings, or debug output, the actual secret value will not be revealed.
// This follows security best practices for handling sensitive data in Go.
//
// Returns:
//   - Always returns the string "[REDACTED]"
func (s PairingSecret) String() string {
	return "[REDACTED]"
}

// Equal compares two pairing secrets using constant-time comparison.
// This prevents timing attacks where an attacker could determine the secret
// by measuring how long the comparison takes. The comparison always takes
// the same amount of time regardless of where the secrets differ.
//
// Parameters:
//   - other: The PairingSecret to compare against
//
// Returns:
//   - true if the secrets are identical, false otherwise
//
// Security: Uses bitwise XOR to ensure constant-time execution
func (s PairingSecret) Equal(other PairingSecret) bool {
	if len(s) != len(other) {
		return false
	}

	var result byte
	for i := range s {
		result |= s[i] ^ other[i]
	}
	return result == 0
}

// IsValidLength reports whether the secret length is acceptable for SHIP pairing.
// This implementation supports 16 bytes (raw 128-bit secret)
func (s PairingSecret) IsValidLength() bool {
	return len(s) == 16
}

/* Configuration Types */

// HMACParams contains parameters for HMAC calculation per SHIP spec section 7
type HMACParams struct {
	Algorithm string          // "hmacSha256" per spec
	Nonce     []byte          // devZ nonce (128-bit)
	TxtRecord *ShipPairingTXT // TXT record for message construction
}

/* State Management */

// PairingState represents the current state of a pairing operation
type PairingState uint

const (
	PairingStateNone       PairingState = iota // No pairing activity
	PairingStateListening                      // devA listening for announcements
	PairingStateAnnouncing                     // devZ announcing to target
	PairingStateValidating                     // Validating received pairing data
	PairingStateCompleted                      // Pairing completed successfully
	PairingStateRejected                       // Pairing rejected (invalid HMAC, etc.)
	PairingStateTimedOut                       // Pairing timed out
	PairingStateError                          // Pairing encountered error
)

/* Status Types */

// AnnouncementStatus represents the status of an announcement operation
type AnnouncementStatus struct {
	Active      bool          // Currently announcing
	Target      PairingTarget // Target device
	StartTime   time.Time     // When announcement started
	Attempts    int           // Number of attempts made
	LastAttempt time.Time     // Last attempt timestamp
	Success     bool          // Whether pairing succeeded
	Error       error         // Last error if any
}

// ListenerStatus represents the status of a listener operation
type ListenerStatus struct {
	Active       bool      // Currently listening
	StartTime    time.Time // When listening started
	RequestsSeen int       // Number of pairing requests seen
	LastRequest  time.Time // Last request timestamp
	Error        error     // Last error if any
}

// PairingTarget represents a target device for pairing announcements
type PairingTarget struct {
	SKI         string // Target device SKI
	Fingerprint string // Target device certificate fingerprint (SHA-256)
	ShipID      string // Target device SHIP ID (for connection)
	Secret      []byte `json:"-"` // Secret for pairing (from QR code SPSEC field), excluded from JSON serialization
}

func (p *PairingTarget) Clear() {
	p.SKI = ""
	p.Fingerprint = ""
	p.ShipID = ""
	clear(p.Secret)
}

func (p *PairingTarget) IsEmpty() bool {
	return len(p.SKI) == 0 || len(p.Fingerprint) == 0 || len(p.ShipID) == 0 || len(p.Secret) == 0
}

// DigestEntry represents a ring buffer entry per SHIP spec section 11
type DigestEntry struct {
	Algorithm string    // HMAC algorithm used
	Digest    string    // HMAC digest value
	Timestamp time.Time // When pairing occurred
}

/* Constants */

// Algorithm and parameter constants per SHIP Pairing Service specification
const (
	AlgorithmHMACSHA256  = "hmacSha256"
	ParTypeFPSHA256      = "fpSha256"
	CommandTypeAddCU     = "addCu"
	CurveSecp256r1       = "secp256r1"
	CurveBrainpoolP256r1 = "brainpoolP256r1"
	CurveBrainpoolP384r1 = "brainpoolP384r1"
)

/* TXT Record Structure */

// ShipPairingTXT represents the TXT record structure for _shippairing._tcp service
// Per SHIP Pairing Service specification Table 1
type ShipPairingTXT struct {
	TxtVers    string // "1" - Version number of TXT format
	ParType    string // "fpSha256" - Parameter type (fingerprint SHA-256)
	ForId      string // devA SHIP ID
	ForPar     string // SHA-256 fingerprint of devA certificate
	TrustId    string // devZ SHIP ID
	TrustPar   string // SHA-256 fingerprint of devZ certificate
	TrustCurve string // "secp256r1" | "brainpoolP256r1" | "brainpoolP384r1"
	Type       string // "addCu" - Command type
	TrustNonce string // devZ nonce (128-bit hex)
	Alg        string // "hmacSha256" - Algorithm
	Digest     string // HMAC result (256-bit hex)
}

// ToMap converts the ShipPairingTXT structure to a map[string]string for mDNS TXT records.
// This method is used when publishing SHIP pairing announcements via mDNS, as TXT records
// are represented as key-value string pairs. The field names match exactly those specified
// in SHIP Pairing Service specification Table 1.
//
// Returns:
//   - A map containing all TXT record fields as string key-value pairs
//   - Keys are lowercase field names (e.g., "txtvers", "parType", "forId")
//   - All values are strings, with binary data hex-encoded
//
// Usage:
//
//	This is typically called by mDNS providers when announcing pairing services
func (sp *ShipPairingTXT) ToMap() map[string]string {
	return map[string]string{
		"txtvers":    sp.TxtVers,
		"parType":    sp.ParType,
		"forId":      sp.ForId,
		"forPar":     sp.ForPar,
		"trustId":    sp.TrustId,
		"trustPar":   sp.TrustPar,
		"trustCurve": sp.TrustCurve,
		"type":       sp.Type,
		"trustNonce": sp.TrustNonce,
		"alg":        sp.Alg,
		"digest":     sp.Digest,
	}
}

// FromMap populates the ShipPairingTXT structure from a map[string]string received from mDNS TXT records.
// This method is used when parsing SHIP pairing announcements discovered via mDNS. It validates
// that all required fields are present and that the txtvers field has the expected value.
//
// Parameters:
//   - txtMap: Map of TXT record key-value pairs received from mDNS discovery
//
// Returns:
//   - nil if parsing succeeds and all required fields are present and valid
//   - PairingValidationError if any required field is missing or txtvers is invalid
//
// Validation:
//   - Checks for presence of all 11 required fields per SHIP spec Table 1
//   - Validates txtvers field equals "1" (current TXT record format version)
//   - Does not validate field content (use Validate() method for that)
//
// Usage:
//
//	This is typically called by mDNS listeners when processing discovered pairing announcements
func (sp *ShipPairingTXT) FromMap(txtMap map[string]string) error {
	// Validate required fields per SHIP spec Table 1
	required := []string{"txtvers", "parType", "forId", "forPar", "trustId",
		"trustPar", "trustCurve", "type", "trustNonce", "alg", "digest"}

	for _, field := range required {
		if _, exists := txtMap[field]; !exists {
			return NewPairingValidationError("missing required TXT field: " + field)
		}
	}

	// Validate txtvers is first and equals "1"
	if txtMap["txtvers"] != "1" {
		return NewPairingValidationError("invalid txtvers, expected '1'")
	}

	// Populate struct fields
	sp.TxtVers = txtMap["txtvers"]
	sp.ParType = txtMap["parType"]
	sp.ForId = txtMap["forId"]
	sp.ForPar = txtMap["forPar"]
	sp.TrustId = txtMap["trustId"]
	sp.TrustPar = txtMap["trustPar"]
	sp.TrustCurve = txtMap["trustCurve"]
	sp.Type = txtMap["type"]
	sp.TrustNonce = txtMap["trustNonce"]
	sp.Alg = txtMap["alg"]
	sp.Digest = txtMap["digest"]

	return nil
}

// Validate checks if all TXT record field values conform to SHIP Pairing Service specification.
// This method validates field content after the structure has been populated (typically after
// calling FromMap). It ensures that enum values are within supported ranges and algorithms
// are supported by this implementation.
//
// Returns:
//   - nil if all field values are valid per SHIP specification
//   - PairingValidationError if any field contains an unsupported or invalid value
//
// Validation checks:
//   - Algorithm must be "hmacSha256" (only supported HMAC algorithm)
//   - Parameter type must be "fpSha256" (fingerprint SHA-256)
//   - Command type must be "addCu" (only supported pairing command)
//   - Trust curve must be one of: secp256r1, brainpoolP256r1, brainpoolP384r1
//
// Usage:
//
//	Call this method after FromMap() to ensure received pairing data is processable
func (sp *ShipPairingTXT) Validate() error {
	// Check required algorithm
	if sp.Alg != AlgorithmHMACSHA256 {
		return NewPairingValidationError("unsupported algorithm: " + sp.Alg)
	}

	// Check parameter type
	if sp.ParType != ParTypeFPSHA256 {
		return NewPairingValidationError("unsupported parameter type: " + sp.ParType)
	}

	// Check command type
	if sp.Type != CommandTypeAddCU {
		return NewPairingValidationError("unsupported command type: " + sp.Type)
	}

	// Check trust curve
	validCurves := []string{CurveSecp256r1, CurveBrainpoolP256r1, CurveBrainpoolP384r1}
	valid := false
	for _, curve := range validCurves {
		if sp.TrustCurve == curve {
			valid = true
			break
		}
	}
	if !valid {
		return NewPairingValidationError("unsupported trust curve: " + sp.TrustCurve)
	}

	return nil
}

/* Error Definitions */

// Custom error types for better error handling
type PairingValidationError struct {
	Field   string
	Message string
}

func (e *PairingValidationError) Error() string {
	if e.Field != "" {
		return "pairing validation error [" + e.Field + "]: " + e.Message
	}
	return "pairing validation error: " + e.Message
}

// NewPairingValidationError creates a new PairingValidationError with a general validation message.
// This constructor is used for validation errors that don't relate to a specific field.
//
// Parameters:
//   - message: Human-readable description of the validation error
//
// Returns:
//   - A new PairingValidationError instance with the specified message
//
// Example usage:
//
//	return NewPairingValidationError("unsupported algorithm: " + alg)
func NewPairingValidationError(message string) *PairingValidationError {
	return &PairingValidationError{Message: message}
}

// NewPairingFieldValidationError creates a new PairingValidationError for a specific field.
// This constructor is used when the validation error relates to a particular field in the
// pairing data structure, providing more context for debugging and error handling.
//
// Parameters:
//   - field: The name of the field that failed validation (e.g., "trustCurve", "digest")
//   - message: Human-readable description of why the field validation failed
//
// Returns:
//   - A new PairingValidationError instance with field-specific error information
//
// Example usage:
//
//	return NewPairingFieldValidationError("trustNonce", "nonce must be 32 hex characters")
func NewPairingFieldValidationError(field, message string) *PairingValidationError {
	return &PairingValidationError{Field: field, Message: message}
}
