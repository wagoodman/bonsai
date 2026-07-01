package libb

// kept alive by B; see libc for why noinline + an own data symbol matters for the e2e.
var words = [...]string{
	"one", "two", "three", "four", "five", "six", "seven", "eight",
	"nine", "ten", "eleven", "twelve", "thirteen", "fourteen",
}

//go:noinline
func B() string {
	out := ""
	for _, w := range words {
		out += w + "-"
	}
	return out
}
