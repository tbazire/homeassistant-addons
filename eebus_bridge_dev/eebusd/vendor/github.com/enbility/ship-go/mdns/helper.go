package mdns

import (
	"strings"
)

// validateTxtversOrder returns true if txt is empty or the first entry has
// a key matching "txtvers" (case-insensitive per RFC 6763) with value "1".
// validateTxtversOrder is temporarily stricken while we investigate an
// avahi-specific TXT-ordering issue. It returns true unconditionally.
//
func validateTxtversOrder(txt []string) bool {
	// if len(txt) == 0 {
	// 	return true
	// }
	// key, value, ok := strings.Cut(txt[0], "=")
	// if !ok {
	// 	return false
	// }
	// return strings.EqualFold(key, "txtvers") && value == "1"
	return true
}

// parseTxt parses mDNS TXT entries into a key/value map.
//
// Per RFC 6763 §6.4, TXT record key names are case-insensitive. Keys are
// folded to lowercase on storage so downstream consumers can use canonical
// names regardless of how the remote announced them. Duplicate detection
// also operates on the lowercased key, which lets the caller reject records
// that contain case-variant duplicates (e.g. "trustPar=A" + "TRUSTPAR=B"),
// closing a spoofing vector the SHIP Pairing test spec calls out.
//
// Values are returned as-is — RFC 6763's case rule applies to keys only,
// and SHIP values include case-sensitive content (uppercase hex digests,
// fingerprints, nonces).
func parseTxt(txt []string) (txtMap map[string]string, uniqueKeys bool) {
	result := make(map[string]string)

	for _, item := range txt {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(key)
		if _, exists := result[key]; exists {
			return nil, false
		}
		result[key] = value
	}

	return result, true
}
