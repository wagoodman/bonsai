package libc

var table = []string{"c1", "c2", "c3", "c4", "c5", "c6"}

//go:noinline
func F() string {
	out := ""
	for _, s := range table {
		out += s + ":"
	}
	return out
}
