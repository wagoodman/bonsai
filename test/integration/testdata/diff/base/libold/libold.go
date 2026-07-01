package libold

var table = []string{"old1", "old2", "old3", "old4", "old5", "old6"}

//go:noinline
func F() string {
	out := ""
	for _, s := range table {
		out += s + ":"
	}
	return out
}
