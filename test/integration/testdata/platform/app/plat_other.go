//go:build !linux && !windows

package main

func platform() string { return "host" }
