package transport

import (
	"errors"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/go/httpclient"
)

// The error classes a record's error attribute carries by default: arrapi's own words,
// which no server text reaches.
const (
	classServerChanged = "server_changed"
	classStatus        = "status"
	classTooLarge      = "too_large"
	classDecode        = "decode"
	classNoID          = "no_id"
	classTransport     = "transport"
)

// ErrorClass is err's class, the first of these its chain holds: server_changed, status,
// too_large, decode, no_id (a command answer without an id of 1 or more); transport for
// any other error, and "" for nil.
func ErrorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case is[*arrapi.ServerChangedError](err):
		return classServerChanged
	case is[*arrapi.StatusError](err):
		return classStatus
	case is[*arrapi.ResponseTooLargeError](err):
		return classTooLarge
	case is[*arrapi.DecodeError](err):
		return classDecode
	case errors.Is(err, errNoCommandID):
		return classNoID
	}
	return classTransport
}

func is[T error](err error) bool {
	_, ok := errors.AsType[T](err)
	return ok
}

// LogError is the value of a record's error attribute for err: its class, or with text
// set its own text, which can quote the server's message, an error answer's body or
// other bytes the server sent (key redacted, single-line, as the error renders).
func LogError(err error, text bool) any {
	if text {
		return httpclient.LogSafeError(err)
	}
	return ErrorClass(err)
}

// StatusCode is the status of the *arrapi.StatusError err's chain holds; 0 when none.
func StatusCode(err error) int {
	if se, ok := errors.AsType[*arrapi.StatusError](err); ok {
		return se.Code
	}
	return 0
}
