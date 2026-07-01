//go:build linux

package main

import "example.com/plat/liblinux"

func platform() string { return liblinux.F() }
