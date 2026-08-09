package main

import "fmt"

// SignatureStatus is checkFileSignature's verdict on one executable.
type SignatureStatus int

const (
	SignatureUnknown  SignatureStatus = iota // platform doesn't support this check (see signatureCheckSupported)
	SignatureUnsigned                        // no Authenticode signature at all
	SignatureValid                           // signed, and the certificate chains to a trusted authority
	SignatureInvalid                         // signed, but something's wrong -- see Detail
)

// SignatureResult is checkFileSignature's result. Detail is a short,
// human-readable reason, set for SignatureInvalid (what's wrong) and
// SignatureUnknown (why the platform can't check).
type SignatureResult struct {
	Status SignatureStatus
	Detail string
}

// formatSignatureResult renders a Check Signature result for the dialog
// shown from procview.go's checkSignatureForSelected. The ✓/⚠/🛑 icons
// mirror Interference Watch's severity vocabulary (see interferenceview.go)
// -- same idea, applied to a different signal (unauthorized/malicious
// binary, not AV/EDR behavior).
func formatSignatureResult(name string, pid int32, exe string, res SignatureResult) string {
	header := fmt.Sprintf("%s (PID %d)\n%s\n\n", name, pid, exe)
	switch res.Status {
	case SignatureValid:
		return header + "✓ Signed with a valid, trusted certificate."
	case SignatureUnsigned:
		return header + "⚠ Not signed. Not necessarily malicious -- plenty of legitimate " +
			"software (especially open-source or in-house tools) ships unsigned -- but " +
			"worth a second look, especially for something you don't recognize."
	case SignatureInvalid:
		return header + fmt.Sprintf("🛑 Signature problem: %s. Could mean the file was "+
			"tampered with after signing, or is signed by an untrusted/unknown authority "+
			"-- worth investigating.", res.Detail)
	default:
		return header + "Code-signing verification isn't available on this platform yet."
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
