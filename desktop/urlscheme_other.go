//go:build !darwin

package main

func getMainBundleIdentifier() (string, error) {
	return "", errURLSchemeHandlerUnsupported
}

func getDefaultHandlerBundleIDForURLScheme(_ string) (string, error) {
	return "", errURLSchemeHandlerUnsupported
}

func setDefaultHandlerBundleIDForURLScheme(_, _ string) error {
	return errURLSchemeHandlerUnsupported
}
