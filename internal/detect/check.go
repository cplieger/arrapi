package detect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Check runs Info's detection and returns its failure as CheckFailure does.
func (s *Session[E]) Check(ctx context.Context) (*transport.Detection, error) {
	d, err := s.Info(ctx)
	if err != nil {
		return nil, CheckFailure(ctx, err)
	}
	return d, nil
}

// CheckFailure is a connection check's error for err, a failure of transport.New or of
// the check's detection: ctx's own error once ctx has ended, since the check did not
// finish, else a *arrapi.CheckError wrapping err.
func CheckFailure(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return &arrapi.CheckError{Err: err, Reason: classify(err)}
}

// classify takes the first matching row: a *ServerChangedError can wrap a
// *StatusError, so it is matched first.
func classify(err error) arrapi.CheckReason {
	if transport.IsKeyInvalid(err) {
		return arrapi.CheckKey
	}
	if ue, ok := errors.AsType[*arrapi.UnsupportedAPIError](err); ok {
		switch ue.Reason {
		case arrapi.ReasonWrongApp:
			return arrapi.CheckWrongApp
		case arrapi.ReasonNotAPI:
			if cutJSON(ue.Err) {
				return arrapi.CheckServer
			}
			return arrapi.CheckNotArrAPI
		default:
			return arrapi.CheckUnsupported
		}
	}
	if _, ok := errors.AsType[*arrapi.ServerChangedError](err); ok {
		return arrapi.CheckServer
	}
	if se, ok := errors.AsType[*arrapi.StatusError](err); ok {
		switch se.Code {
		case http.StatusUnauthorized, http.StatusForbidden:
			return arrapi.CheckKey
		case http.StatusNotFound:
			return arrapi.CheckBasePath
		case http.StatusBadRequest:
			return arrapi.CheckURL
		default:
			return arrapi.CheckServer
		}
	}
	return arrapi.CheckURL
}

// cutJSON reports whether err holds a *DecodeError of an answer that claims to be JSON
// and does not parse: a server that cut its answer, not another API's well-formed JSON.
func cutJSON(err error) bool {
	de, ok := errors.AsType[*arrapi.DecodeError](err)
	if !ok || !isJSONMediaType(de.ContentType) {
		return false
	}
	_, syntax := errors.AsType[*json.SyntaxError](de.Err)
	return syntax
}

// isJSONMediaType matches application/json and any type/subtype+json (RFC 6839),
// folding ASCII case only.
func isJSONMediaType(mt string) bool {
	if asciiEqualFold(mt, "application/json") {
		return true
	}
	const suffix = "+json"
	typ, sub, ok := strings.Cut(mt, "/")
	return ok && typ != "" && len(sub) > len(suffix) && asciiEqualFold(sub[len(sub)-len(suffix):], suffix)
}
