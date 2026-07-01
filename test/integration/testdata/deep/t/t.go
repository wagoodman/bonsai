package t

var table = [...]string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"}

//go:noinline
func T() string {
	out := ""
	for _, x := range table {
		out += x + ";"
	}
	return out
}
