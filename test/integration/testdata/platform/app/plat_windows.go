//go:build windows

package main

import "example.com/plat/libwin"

func platform() string { return libwin.F() }
