// Package decodehook lets internal/transport build an *arrapi.DecodeError, whose text
// fields are unexported; arrapi's init assigns New before any importer can call it.
package decodehook

// Fields is every value of one DecodeError.
type Fields struct {
	Err                                      error
	Method, Path, ContentType, Field, Reason string
	Status                                   int
}

// New returns the *arrapi.DecodeError holding f.
var New func(f Fields) error
