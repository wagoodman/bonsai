package bonsai

// shared string literals used across the analysis passes. Centralized so the values that
// carry domain meaning (and recur across files) live in one place.
const (
	// pkgMain is the Go package name of a build entrypoint, and the class label for the main module.
	pkgMain = "main"
	// modStd buckets standard-library weight, which has no owning module.
	modStd = "std"
	// pkgGenerated marks symbols with no real owning package (compiler-generated, type metadata).
	pkgGenerated = "<generated>"
	// sectionText is the executable code section name across binary formats.
	sectionText = ".text"
)
