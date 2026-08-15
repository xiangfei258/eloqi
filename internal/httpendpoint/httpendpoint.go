// Package httpendpoint validates user-configured HTTP service endpoints
// without echoing their potentially sensitive raw contents in errors.
package httpendpoint

import (
	"errors"
	"net/url"
	"strings"
)

// Validate requires an absolute HTTP(S) URL with a host. Fragments and
// relative request targets are rejected by ParseRequestURI.
func Validate(raw string) error {
	raw = strings.TrimSpace(raw)
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || strings.Contains(raw, "#") || parsed.Host == "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("must be an absolute http or https URL with a host")
	}
	return nil
}
