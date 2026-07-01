package liba

import "example.com/diff/libc"

//go:noinline
func A() string { return "a:" + libc.F() }
