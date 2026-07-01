package libnew

var table = []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8"}

//go:noinline
func g1() string {
	out := ""
	for _, s := range table {
		out += s + "=g1"
	}
	return out
}

//go:noinline
func g2() string {
	out := ""
	for i, s := range table {
		if i > 3 {
			out += s + "-hi"
		} else {
			out += s + "-lo"
		}
	}
	return out
}

//go:noinline
func F() string { return g1() + g2() }
