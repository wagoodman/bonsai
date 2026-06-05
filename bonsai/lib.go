/*
Package bonsai attributes the size of a compiled Go binary to its module dependencies and
estimates the cost/benefit of pruning each direct dependency.

It joins three signals that no single existing tool combines:
  - size:       per-module code bytes in the linked binary
  - tree-shake: bytes (and transitive modules) freed if a direct dep were removed
  - coupling:   how deeply first-party code is wired into each dep (removal effort)

By default it builds the target from source (capturing the linker's -dumpdep reachability
graph for exact, post-dead-code-elimination tree-shaking) and analyzes the artifact it
produced; a prebuilt binary can be analyzed instead via Config.Binary. Size attribution
parses the binary's symbol table and gopclntab via debug/gosym (the latter works even on
stripped binaries), and the graph/coupling analyses use `go list` and a go/parser AST scan.
*/
package bonsai

import (
	"github.com/anchore/go-logger"
	"github.com/anchore/go-logger/adapter/redact"
	"github.com/wagoodman/go-partybus"

	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/log"
	intRedact "github.com/wagoodman/bonsai/internal/redact"
)

// SetLogger sets the package-level logger used by the library. Library consumers may call
// this to route bonsai's log output into their own logger.
func SetLogger(logger logger.Logger) {
	useOrAddRedactor()
	log.Set(logger)
}

// SetBus sets the event bus the library publishes progress and report events onto.
func SetBus(b *partybus.Bus) {
	useOrAddRedactor()
	bus.Set(b)
}

// useOrAddRedactor ensures a redaction store exists before the logger/bus are configured,
// so redaction works even when the library is driven outside the CLI application.
func useOrAddRedactor() {
	store := intRedact.Get()
	if store == nil {
		intRedact.Set(redact.NewStore())
	}
}
