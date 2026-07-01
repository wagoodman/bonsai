package libwin

var table = [...]string{"win1", "win2", "win3", "win4", "win5", "win6"}

//go:noinline
func F() string {
	out := ""
	for _, s := range table {
		out += s + ":"
	}
	return out
}
