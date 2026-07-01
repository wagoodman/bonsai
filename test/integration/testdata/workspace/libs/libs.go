package libs

import "example.com/e2e/libc"

//go:noinline
func S() string { return "s:" + libc.C() }
