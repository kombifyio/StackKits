package main

import "golang.org/x/sys/unix"

// flushInput drops what was typed (or sent) before a question was asked:
// some terminals send a line feed after the Enter that started the
// command, which would otherwise read as an empty answer ("No").
func flushInput(fd int) {
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
