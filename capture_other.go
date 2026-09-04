//go:build !windows

package main

import "errors"

// captureSupported gates captureview.go's Capture Trace window -- WPR
// (Windows Performance Recorder) is Windows-only. macOS/Linux have their
// own trace tooling (Instruments, perf/ftrace) but wiring either up isn't
// implemented -- same "Windows-first" scoping as Check Signature and
// thread start-address resolution.
const captureSupported = false

func startCapture([]string) error {
	return errors.New("trace capture isn't available on this platform yet")
}

func stopCapture(string) error {
	return errors.New("trace capture isn't available on this platform yet")
}

func cancelCapture() error {
	return nil
}

func captureIsActive() bool {
	return false
}

func openCaptureFileLocation(string) error {
	return errors.New("not available on this platform yet")
}

func openCaptureInWPA(string) error {
	return errors.New("not available on this platform yet")
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
