//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// signatureCheckSupported gates procview.go's "Check Signature" button --
// see signature_other.go for the disabled stub on macOS/Linux.
const signatureCheckSupported = true

// checkFileSignature runs an Authenticode check via WinVerifyTrust, the
// same API Explorer's "Digital Signatures" tab and signtool /verify use --
// not a from-scratch parse of the PKCS#7 signature blob. RevocationChecks
// is deliberately WTD_REVOKE_NONE: checking revocation would mean a network
// hit (a CRL/OCSP fetch) per check, which conflicts with this app's
// lightweight-by-design, offline-first stance elsewhere (see CLAUDE.md) --
// this is a local, structural check (is it signed, does the chain lead to
// a trusted root), not a live "has this cert been revoked since" verdict.
//
// A plain WinVerifyTrust-on-the-file call alone is not enough: confirmed by
// testing against a real Windows 11 install that most System32 binaries
// (e.g. notepad.exe) have no *embedded* signature at all -- they're
// "catalog-signed" instead, verified against a separate signed .cat file
// via the CryptCATAdmin* family, the same fallback signtool/sigcheck/
// PowerShell's Get-AuthenticodeSignature use. Skipping it would flag a huge
// share of stock Windows as "unsigned", the opposite of useful. So: try the
// direct check first, and only report Unsigned if the catalog check (see
// isCatalogSigned) also comes back empty.
func checkFileSignature(path string) (SignatureResult, error) {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return SignatureResult{}, err
	}

	fileInfo := &windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: path16,
	}
	data := &windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(fileInfo),
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)

	// Always release the verification state WinVerifyTrust allocated,
	// regardless of the verify outcome above -- skipping this leaks
	// provider state per call.
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)

	switch {
	case verifyErr == nil:
		return SignatureResult{Status: SignatureValid}, nil
	case verifyErr == windows.Errno(windows.TRUST_E_NOSIGNATURE):
		if isCatalogSigned(path) {
			return SignatureResult{Status: SignatureValid}, nil
		}
		return SignatureResult{Status: SignatureUnsigned}, nil
	default:
		return SignatureResult{Status: SignatureInvalid, Detail: signatureErrorDetail(verifyErr)}, nil
	}
}

// wintrust's CryptCATAdmin* family isn't wrapped by golang.org/x/sys/windows
// (unlike WinVerifyTrustEx), so it's declared the same way this codebase
// already declares other undocumented/unwrapped Windows APIs it needs -- a
// LazyDLL proc lookup (see threads_windows.go's ntdll vars).
var (
	wintrustDLL                              = windows.NewLazySystemDLL("wintrust.dll")
	procCryptCATAdminAcquireContext2         = wintrustDLL.NewProc("CryptCATAdminAcquireContext2")
	procCryptCATAdminCalcHashFromFileHandle2 = wintrustDLL.NewProc("CryptCATAdminCalcHashFromFileHandle2")
	procCryptCATAdminEnumCatalogFromHash     = wintrustDLL.NewProc("CryptCATAdminEnumCatalogFromHash")
	procCryptCATAdminReleaseCatalogContext   = wintrustDLL.NewProc("CryptCATAdminReleaseCatalogContext")
	procCryptCATAdminReleaseContext          = wintrustDLL.NewProc("CryptCATAdminReleaseContext")
)

// isCatalogSigned reports whether path's SHA-256 hash is listed in any
// catalog the system's catalog database already knows about -- Windows only
// ever learns of a catalog by verifying its own embedded signature first
// (at driver/component install time), so a hit here is as trustworthy as a
// direct embedded-signature check, just one level removed. Returns false
// (never an error) for anything short of a clean match: this only ever runs
// as checkFileSignature's fallback after a direct check already found
// nothing, so any failure here just means "still unsigned," not a new kind
// of error to surface.
func isCatalogSigned(path string) bool {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(path16, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	alg, err := windows.UTF16PtrFromString("SHA256")
	if err != nil {
		return false
	}
	var hCatAdmin uintptr
	ok, _, _ := procCryptCATAdminAcquireContext2.Call(
		uintptr(unsafe.Pointer(&hCatAdmin)),
		0, // pgSubsystem: NULL = any
		uintptr(unsafe.Pointer(alg)),
		0, // pStrongHashPolicy: NULL
		0, // dwFlags
	)
	if ok == 0 || hCatAdmin == 0 {
		return false
	}
	defer procCryptCATAdminReleaseContext.Call(hCatAdmin, 0)

	// First call with a nil buffer just to learn the hash size.
	var hashSize uint32
	procCryptCATAdminCalcHashFromFileHandle2.Call(
		hCatAdmin, uintptr(h), uintptr(unsafe.Pointer(&hashSize)), 0, 0,
	)
	if hashSize == 0 {
		return false
	}
	hash := make([]byte, hashSize)
	ok, _, _ = procCryptCATAdminCalcHashFromFileHandle2.Call(
		hCatAdmin, uintptr(h), uintptr(unsafe.Pointer(&hashSize)), uintptr(unsafe.Pointer(&hash[0])), 0,
	)
	if ok == 0 {
		return false
	}

	hCatInfo, _, _ := procCryptCATAdminEnumCatalogFromHash.Call(
		hCatAdmin, uintptr(unsafe.Pointer(&hash[0])), uintptr(hashSize), 0, 0,
	)
	if hCatInfo == 0 {
		return false
	}
	defer procCryptCATAdminReleaseCatalogContext.Call(hCatAdmin, hCatInfo, 0)

	return true
}

// signatureErrorDetail maps the handful of WinVerifyTrust outcomes worth a
// specific explanation to one; anything else falls back to the raw error
// text rather than pretending to have a friendlier answer.
func signatureErrorDetail(err error) string {
	switch err {
	case windows.Errno(windows.CERT_E_UNTRUSTEDROOT):
		return "untrusted root certificate (self-signed or a private/internal CA)"
	case windows.Errno(windows.CERT_E_EXPIRED):
		return "signing certificate expired"
	case windows.Errno(windows.CERT_E_REVOKED):
		return "certificate revoked"
	case windows.Errno(windows.TRUST_E_BAD_DIGEST):
		return "signature/hash mismatch -- file modified after signing"
	case windows.Errno(windows.TRUST_E_EXPLICIT_DISTRUST):
		return "explicitly distrusted by this system's policy"
	case windows.Errno(windows.TRUST_E_SUBJECT_NOT_TRUSTED):
		return "explicitly not trusted"
	case windows.Errno(windows.CERT_E_CHAINING), windows.Errno(windows.CERT_E_UNTRUSTEDCA):
		return "certificate chain doesn't lead to a trusted authority"
	default:
		return err.Error()
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
