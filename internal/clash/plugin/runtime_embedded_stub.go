//go:build !proxy

package clashplugin

import (
	"fmt"
	"io"
)

func startEmbeddedRuntimeInstance(_ []byte, _ string) (io.Closer, error) {
	return nil, fmt.Errorf("embedded clash runtime is not available in this build")
}
