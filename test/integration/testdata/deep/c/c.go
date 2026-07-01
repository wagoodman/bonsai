package c

import (
	"example.com/deep/s"
	"example.com/deep/t"
)

//go:noinline
func Do() string { return "c:" + s.S() + t.T() }
