package a

import "example.com/deep/s"

//go:noinline
func Do() string { return "a:" + s.S() }
