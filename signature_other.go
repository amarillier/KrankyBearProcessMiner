//go:build !windows

package main

import "errors"

// signatureCheckSupported gates procview.go's "Check Signature" button --
// Authenticode verification is a Windows API (WinVerifyTrust, see
// signature_windows.go); macOS has an equivalent (codesign/Security.framework)
// but it's a separate, unimplemented piece of work, same "Windows-first"
// scoping as thread start-address resolution (see threads_other.go).
const signatureCheckSupported = false

func checkFileSignature(path string) (SignatureResult, error) {
	return SignatureResult{Status: SignatureUnknown}, errors.New("code-signing verification isn't available on this platform yet")
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
