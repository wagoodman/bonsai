package libcommon

var table = [...]string{"common1", "common2", "common3", "common4", "common5", "common6"}

//go:noinline
func F() string {
	out := ""
	for _, s := range table {
		out += s + ":"
	}
	return out
}
