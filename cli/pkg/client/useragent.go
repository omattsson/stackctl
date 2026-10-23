package client

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
)

// version is the stackctl version in the User-Agent header. main sets it
// once at startup through cmd.SetVersionInfo and SetVersion.
var version atomic.Value // string

// SetVersion sets the stackctl version that every request reports in its
// User-Agent header. An empty version means "dev".
func SetVersion(v string) {
	version.Store(v)
}

// UserAgent returns the User-Agent header value of every request:
// "stackctl/<version> (<os>/<arch>)".
func UserAgent() string {
	v, _ := version.Load().(string)
	return formatUserAgent(v)
}

// formatUserAgent builds the header value. Every byte of v that is not an
// RFC 9110 tchar becomes "_", so a bad version (a space, a newline) cannot
// break the request or split the product token.
func formatUserAgent(v string) string {
	if v == "" {
		v = "dev"
	}
	b := []byte(v)
	for i, c := range b {
		if !isTchar(c) {
			b[i] = '_'
		}
	}
	return fmt.Sprintf("stackctl/%s (%s/%s)", b, runtime.GOOS, runtime.GOARCH)
}

// isTchar reports whether c is an RFC 9110 token character.
func isTchar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// setUserAgent sets the User-Agent header on h.
func setUserAgent(h http.Header) {
	h.Set("User-Agent", UserAgent())
}
