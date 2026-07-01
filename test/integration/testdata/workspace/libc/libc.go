package libc

// Table is an exported data symbol kept alive by C; together with the //go:noinline below it
// guarantees libc contributes its own durable, attributable weight to the binary (so the e2e
// can assert size relations and reachability structure instead of hoping nothing inlined away).
var Table = [...]string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel",
	"india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa",
}

//go:noinline
func C() string {
	out := ""
	for _, s := range Table {
		out += s + ":"
	}
	return out
}
