package workspace

import (
	"encoding/base32"
	"encoding/hex"
	"regexp"
	"strings"
)

var instanceUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

// WorkspaceName retains every UUID bit while satisfying Coder's 32-character
// limit. It is immutable and independent of the user's display name.
func WorkspaceName(instance string) (string, error) {
	if !instanceUUID.MatchString(instance) {
		return "", ErrUnavailable
	}
	data, err := hex.DecodeString(strings.ReplaceAll(instance, "-", ""))
	if err != nil {
		return "", ErrUnavailable
	}
	return "hc-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)), nil
}
