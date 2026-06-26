// Package humanize formats machine quantities for human-readable reports. It is shared by the
// static report renderer and the interactive explorer so binary sizes read identically in both.
package humanize

import "fmt"

// Bytes renders a byte count in decimal (SI, 1000-based) units — the convention Go binary-size
// tooling uses — e.g. 1.5 MB, 27.8 MB. Values below 1 kB are shown as exact bytes.
func Bytes(b uint64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGT"[exp])
}
