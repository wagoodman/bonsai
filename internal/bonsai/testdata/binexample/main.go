// Package main is a tiny, stdlib-only program compiled by the binary-attribution tests
// (see binary_test.go). It is cross-compiled to each supported OS so the Mach-O, ELF, and
// PE readers are all exercised regardless of the host platform. It lives under testdata so the
// parent module's tooling ignores its nested go.mod.
package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.ToUpper("hello bonsai"))
}
