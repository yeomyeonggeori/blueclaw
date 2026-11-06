package runtimecontrol

import "golang.org/x/sys/unix"

func kernelDoubtsTheClock() bool {
	state, errorValue := unix.Adjtimex(&unix.Timex{})
	return errorValue != nil || state == unix.TIME_ERROR
}
