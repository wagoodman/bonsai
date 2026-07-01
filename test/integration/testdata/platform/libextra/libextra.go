package libextra

var table = [...]string{"extra1", "extra2", "extra3", "extra4", "extra5", "extra6"}

//go:noinline
func F() string {
	out := ""
	for _, s := range table {
		out += s + ":"
	}
	return out
}
