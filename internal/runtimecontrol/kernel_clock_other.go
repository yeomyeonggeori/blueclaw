//go:build !linux

package runtimecontrol

func kernelDoubtsTheClock() bool {
	return false
}
