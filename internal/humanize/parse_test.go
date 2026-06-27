package humanize

import "testing"

func TestParseBytesRoundTrip(t *testing.T) {
	// values chosen so Bytes renders without lossy rounding, so ParseBytes(Bytes(v)) == v.
	for _, v := range []uint64{0, 512, 1000, 25_000_000, 2_000_000, 1_500_000_000} {
		s := Bytes(v)
		got, err := ParseBytes(s)
		if err != nil {
			t.Fatalf("ParseBytes(%q): %v", s, err)
		}
		if got != v {
			t.Errorf("round trip %d -> %q -> %d", v, s, got)
		}
	}
}

func TestParseBytes(t *testing.T) {
	ok := map[string]uint64{
		"25MB":   25_000_000,
		"25 mb":  25_000_000,
		"2MB":    2_000_000,
		"1024":   1024,
		"1KB":    1000,
		"1.5 GB": 1_500_000_000,
		"512 B":  512,
	}
	for in, want := range ok {
		got, err := ParseBytes(in)
		if err != nil {
			t.Errorf("ParseBytes(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseBytes(%q) = %d, want %d", in, got, want)
		}
	}

	for _, in := range []string{"", "abc", "MB", "10XB", "-5MB"} {
		if _, err := ParseBytes(in); err == nil {
			t.Errorf("ParseBytes(%q): expected error", in)
		}
	}
}
