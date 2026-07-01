package s

import "example.com/deep/t"

var table = [...]string{"s1", "s2", "s3", "s4", "s5", "s6"}

//go:noinline
func S() string {
	out := "s:" + t.T()
	for _, x := range table {
		out += x + ","
	}
	return out
}
