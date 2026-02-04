//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation -framework CoreServices -framework CoreFoundation

#include <CoreServices/CoreServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <AppKit/AppKit.h>
#include <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

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

static char* default_handler_bundle_id_for_url_scheme(const char* scheme) {
	@autoreleasepool {
		if (scheme == NULL) {
			return NULL;
		}

		NSString *schemeStr = [NSString stringWithUTF8String:scheme];
		if (schemeStr == nil) {
			return NULL;
		}

		NSString *urlStr = [NSString stringWithFormat:@"%@://", schemeStr];
		NSURL *url = [NSURL URLWithString:urlStr];
		if (url == nil) {
			return NULL;
		}

		NSURL *appURL = [[NSWorkspace sharedWorkspace] URLForApplicationToOpenURL:url];
		if (appURL == nil) {
			return NULL;
		}

		NSBundle *bundle = [NSBundle bundleWithURL:appURL];
		if (bundle == nil) {
			return NULL;
		}

		NSString *bundleID = [bundle bundleIdentifier];
		if (bundleID == nil) {
			return NULL;
		}

		const char *utf8 = [bundleID UTF8String];
		if (utf8 == NULL) {
			return NULL;
		}
		return strdup(utf8);
	}
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

	cstr := C.default_handler_bundle_id_for_url_scheme(cscheme)
	if cstr == nil {
		return "", nil
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
