package b

import "example.com/deep/s"

//go:noinline
func Do() string { return "b:" + s.S() }
