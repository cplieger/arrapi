package transport

import (
	"net/http"
	"strconv"
	"strings"
)

const headerAppVersion = "X-Application-Version"

// Identity is the server a request is sent to, as the detection that chose its engine
// verified it from a status answer.
type Identity struct {
	App        string // "Sonarr" or "Radarr"
	AppVersion string // that status answer's well-formed X-Application-Version; "" for a probe before its run anchored
}

// ParseAppVersion returns h's X-Application-Version when it is well-formed: exactly one
// value of two to four dot-separated runs of 1 to 9 digits, the form .NET's
// System.Version.ToString writes; ok is false otherwise.
func ParseAppVersion(h http.Header) (v string, major int, ok bool) {
	values := h.Values(headerAppVersion)
	if len(values) != 1 {
		return "", 0, false
	}
	v = values[0]
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return "", 0, false
	}
	for _, p := range parts {
		if len(p) < 1 || len(p) > 9 || strings.Trim(p, "0123456789") != "" {
			return "", 0, false
		}
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", 0, false
	}
	return v, major, true
}
