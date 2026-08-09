//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckFileSignature exercises the real WinVerifyTrust call against a
// known-signed system binary and a known-unsigned file, rather than trusting
// that unfamiliar Windows API glue compiles cleanly and also behaves --
// see signature_windows.go's checkFileSignature.
func TestCheckFileSignature(t *testing.T) {
	sys32 := os.Getenv("SystemRoot")
	if sys32 == "" {
		sys32 = `C:\Windows`
	}
	signed := filepath.Join(sys32, "System32", "notepad.exe")
	if _, err := os.Stat(signed); err != nil {
		t.Skipf("expected system binary not found: %v", err)
	}
	res, err := checkFileSignature(signed)
	if err != nil {
		t.Fatalf("checkFileSignature(%q): %v", signed, err)
	}
	if res.Status != SignatureValid {
		t.Errorf("checkFileSignature(%q) = %+v, want SignatureValid", signed, res)
	}

	// The compiled test binary itself: a real, valid PE (unlike a hand-faked
	// file WinVerifyTrust would reject as an unrecognized subject form
	// before even getting to "is it signed"), and definitely unsigned since
	// `go test` doesn't sign its output.
	unsigned, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	res, err = checkFileSignature(unsigned)
	if err != nil {
		t.Fatalf("checkFileSignature(%q): %v", unsigned, err)
	}
	if res.Status != SignatureUnsigned {
		t.Errorf("checkFileSignature(%q) = %+v, want SignatureUnsigned", unsigned, res)
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
