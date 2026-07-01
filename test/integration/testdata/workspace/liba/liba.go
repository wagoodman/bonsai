package liba

import (
	"example.com/e2e/libb"
	"example.com/e2e/libc"
)

//go:noinline
func A() string { return libb.B() + "|" + libc.C() }
