//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreServices -framework CoreFoundation

#include <CoreServices/CoreServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

static char* cfstring_to_cstring(CFStringRef s) {
	if (s == NULL) {
		return NULL;
	}
	CFIndex length = CFStringGetLength(s);
	CFIndex maxSize = CFStringGetMaximumSizeForEncoding(length, kCFStringEncodingUTF8) + 1;
	char *buffer = (char *)malloc(maxSize);
	if (buffer == NULL) {
		return NULL;
	}
	if (CFStringGetCString(s, buffer, maxSize, kCFStringEncodingUTF8)) {
		return buffer;
	}
	free(buffer);
	return NULL;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

func getMainBundleIdentifier() (string, error) {
	bundle := C.CFBundleGetMainBundle()
	if bundle == 0 {
		return "", errors.New("main bundle not available (not running as a bundled app)")
	}

	identifier := C.CFBundleGetIdentifier(bundle)
	if identifier == 0 {
		return "", errors.New("bundle identifier not found")
	}

	cstr := C.cfstring_to_cstring(identifier)
	if cstr == nil {
		return "", errors.New("failed to convert bundle identifier to string")
	}
	defer C.free(unsafe.Pointer(cstr))

	return C.GoString(cstr), nil
}

func getDefaultHandlerBundleIDForURLScheme(scheme string) (string, error) {
	cscheme := C.CString(scheme)
	defer C.free(unsafe.Pointer(cscheme))

	cfScheme := C.CFStringCreateWithCString(C.kCFAllocatorDefault, cscheme, C.kCFStringEncodingUTF8)
	if cfScheme == 0 {
		return "", errors.New("failed to create CFString for scheme")
	}
	defer C.CFRelease(C.CFTypeRef(cfScheme))

	handler := C.LSCopyDefaultHandlerForURLScheme(cfScheme)
	if handler == 0 {
		return "", nil
	}
	defer C.CFRelease(C.CFTypeRef(handler))

	cstr := C.cfstring_to_cstring(handler)
	if cstr == nil {
		return "", errors.New("failed to convert default handler bundle id to string")
	}
	defer C.free(unsafe.Pointer(cstr))

	return C.GoString(cstr), nil
}

func setDefaultHandlerBundleIDForURLScheme(scheme, bundleID string) error {
	cscheme := C.CString(scheme)
	defer C.free(unsafe.Pointer(cscheme))
	cbundle := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cbundle))

	cfScheme := C.CFStringCreateWithCString(C.kCFAllocatorDefault, cscheme, C.kCFStringEncodingUTF8)
	if cfScheme == 0 {
		return errors.New("failed to create CFString for scheme")
	}
	defer C.CFRelease(C.CFTypeRef(cfScheme))

	cfBundle := C.CFStringCreateWithCString(C.kCFAllocatorDefault, cbundle, C.kCFStringEncodingUTF8)
	if cfBundle == 0 {
		return errors.New("failed to create CFString for bundle id")
	}
	defer C.CFRelease(C.CFTypeRef(cfBundle))

	status := C.LSSetDefaultHandlerForURLScheme(cfScheme, cfBundle)
	if status != 0 {
		return fmt.Errorf("LSSetDefaultHandlerForURLScheme failed: %d", int(status))
	}
	return nil
}
