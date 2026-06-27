package humanize

import (
	"fmt"
	"strconv"
	"strings"
)

// unitFactor maps a byte-size unit suffix to its decimal (SI, 1000-based) multiplier — the same
// convention Bytes formats with. "B" and the empty suffix mean raw bytes.
var unitFactor = map[string]uint64{
	"":   1,
	"B":  1,
	"KB": 1e3,
	"MB": 1e6,
	"GB": 1e9,
	"TB": 1e12,
}

// ParseBytes is the inverse of Bytes: it parses a human byte-size string ("25MB", "2 MB", "1024")
// into a byte count. Units are decimal (1 MB = 1,000,000 bytes), case-insensitive, with optional
// space before the unit. The companion to Bytes so a budget written "25MB" round-trips.
func ParseBytes(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}

	// split the trailing unit letters from the leading number.
	i := len(s)
	for i > 0 && (s[i-1] < '0' || s[i-1] > '9') && s[i-1] != '.' {
		i--
	}
	num := strings.TrimSpace(s[:i])
	unit := strings.ToUpper(strings.TrimSpace(s[i:]))

	factor, ok := unitFactor[unit]
	if !ok {
		return 0, fmt.Errorf("unknown size unit %q in %q", unit, s)
	}
	val, err := strconv.ParseFloat(num, 64)
	if err != nil || val < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return uint64(val * float64(factor)), nil
}
