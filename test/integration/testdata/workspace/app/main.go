package main

import (
	"fmt"

	"example.com/e2e/liba"
	"example.com/e2e/libs"
	"example.com/e2e/libz"
)

func main() {
	fmt.Println(liba.A())
	fmt.Println(libs.S())
}

// deadImport references libz but is never called, so the linker dead-code-eliminates it and
// libz never makes it into the binary, even though `go list` reports app importing libz. the
// e2e uses this to prove DCE-dropped source imports are excluded from the analysis.
//
//nolint:unused
func deadImport() string { return libz.Z() }
