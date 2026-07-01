package libc

var table = []string{"c1", "c2", "c3", "c4", "c5", "c6"}

//go:noinline
func h1() string {
	out := ""
	for _, s := range table {
		out += s + "-h1"
	}
	return out
}

//go:noinline
func h2() string {
	out := ""
	for i, s := range table {
		if i%2 == 0 {
			out += s + "-even"
		} else {
			out += s + "-odd"
		}
	}
	return out
}

//go:noinline
func h3() string {
	out := ""
	for _, s := range table {
		out = s + ":" + out
	}
	return out
}

//go:noinline
func F() string { return h1() + h2() + h3() }
