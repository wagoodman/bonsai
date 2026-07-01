package liblinux

var table = [...]string{"lin1", "lin2", "lin3", "lin4", "lin5", "lin6"}

//go:noinline
func F() string {
	out := ""
	for _, s := range table {
		out += s + ":"
	}
	return out
}
