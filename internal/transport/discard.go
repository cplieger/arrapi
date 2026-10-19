package transport

import (
	"io"
	"net/http"
)

// maxDiscardBytes is net/http's own bound for a redirect body it throws away
// (maxBodySlurpSize, net/http/client.go): enough to return a short body's connection
// to the pool, never a read of an unbounded one.
const maxDiscardBytes = 2 << 10

// discardBody is the only code that throws a body away. A body declared longer than
// maxDiscardBytes is closed unread.
func discardBody(resp *http.Response) {
	if resp.ContentLength == -1 || resp.ContentLength <= maxDiscardBytes {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDiscardBytes))
	}
	_ = resp.Body.Close()
}
