//go:build windows

package secrets

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DPAPI, scoped to the current user. The ciphertext is bound to this Windows
// account, so copying vault.bin to another machine or another user profile
// yields nothing — which is the property we want from "encrypted at rest"
// without asking the user to invent and remember a passphrase.

var (
	crypt32                = windows.NewLazySystemDLL("crypt32.dll")
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

func (b dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

// entropy ties the ciphertext to this application, so another program running
// as the same user cannot simply hand the blob back to DPAPI and read it.
var entropy = []byte("go-ai-team.vault.v1")

// cryptProtectUIForbidden tells DPAPI never to show a prompt; this process may
// be running headless.
const cryptProtectUIForbidden = 0x1

func seal(plain []byte) ([]byte, error) {
	in := newBlob(plain)
	ent := newBlob(entropy)
	var out dataBlob
	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)), 0,
		uintptr(unsafe.Pointer(&ent)), 0, 0, cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData failed: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unseal(sealed []byte) ([]byte, error) {
	in := newBlob(sealed)
	ent := newBlob(entropy)
	var out dataBlob
	r, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)), 0,
		uintptr(unsafe.Pointer(&ent)), 0, 0, cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
