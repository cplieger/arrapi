package transport

import (
	"context"
	"strconv"
)

// Accepted reports whether r is an accepted answer that decoded: the one rule a read's
// value, and a by-id read's found flag, rest on.
func (r Result) Accepted() bool { return r.Verdict == VerdictAccept && r.Err == nil }

// GetList reads the list at path in one attempt and maps every item; on any result
// but Accepted it returns no items. A null item is a decode error, since no resource
// list holds one and decoding it would hand the caller a zero-value resource.
func GetList[W, T any](ctx context.Context, c *Client, d *Detection, path string, m func(*W) T) ([]T, Result) {
	var wire []*W
	r := c.GetJSON(ctx, d, path, MaxListBytes, &wire)
	if !r.Accepted() {
		return nil, r
	}
	out := make([]T, len(wire))
	for i, w := range wire {
		if w == nil {
			r.Err = &decodeError{path: stripQuery(path), reason: "list item " + strconv.Itoa(i) + " is null"}
			return nil, r
		}
		out[i] = m(w)
	}
	return out, r
}

// GetOne reads one resource by id in one attempt and maps it; absence is r's
// VerdictAbsent, and on any result but Accepted it returns the zero value.
func GetOne[W, T any](ctx context.Context, c *Client, d *Detection, path string, m func(*W) T) (T, Result) {
	var w W
	r := c.GetByID(ctx, d, path, &w)
	if !r.Accepted() {
		var zero T
		return zero, r
	}
	return m(&w), r
}
