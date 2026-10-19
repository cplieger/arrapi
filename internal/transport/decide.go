package transport

import "net/http"

// Call is what a request asks for, which decides what its answer may mean.
type Call int

// The calls arrapi sends.
const (
	CallProbe    Call = iota + 1 // a detection GET
	CallRead                     // a list or query GET
	CallReadByID                 // a GET of one resource by id: its attributed 404 means absent
	CallMutation                 // a command POST, sent once
)

// End is how one attempt ended, as endAttempt selected it.
type End struct {
	Header    http.Header // EndAnswer: the answer's header
	Kind      EndKind
	Code      int  // EndAnswer: the answer's status
	Transient bool // EndTransport: httpx.IsTransient of the safe error
	HopFailed bool // EndTransport: a header-less 301, 302, 303, 307 or 308 whose hop failed marked the detection stale; never set for an unanchored probe
}

// EndKind is the kind of an attempt's terminal outcome.
//
//deadset:ignore DS1101 -- the type of End.Kind, whose exported constants the black-box decide tests build End values from
type EndKind int

// The kinds of terminal outcome.
const (
	EndAnswer    EndKind = iota + 1 // an answer, a redirect net/http discarded after a malformed Location included
	EndNotSent                      // the send check refused the attempt or one of its hops
	EndTransport                    // a transport error, a refused redirect included
)

// Verdict is what an attempt's outcome means for its call.
type Verdict int

// The verdicts.
const (
	VerdictAccept       Verdict = iota + 1 // a success: decode it; for a mutation, the command's success
	VerdictStatus                          // not a success, from the server sent to (or before the run anchored): its *arrapi.StatusError
	VerdictUnidentified                    // not a success and without the header: its *arrapi.StatusError
	VerdictAbsent                          // a 404 to a by-id read from the server it was sent to: the item is gone
	VerdictChanged                         // another server version answered: *arrapi.ServerChangedError
	VerdictNoHeader                        // *arrapi.UnsupportedAPIError{Reason: ReasonNoVersionHeader}
	VerdictUnconfirmed                     // a command's 2xx it cannot attribute: wraps ErrCommandOutcomeUnknown
	VerdictRefused                         // not sent: the send check's *arrapi.ServerChangedError
	VerdictTransport                       // a transport error, made safe
)

// Next is the attempt loop's step after one attempt, before the call's budget applies.
type Next int

// The steps.
const (
	NextDone  Next = iota + 1 // return the attempt's result
	NextRetry                 // send again on the same detection
	NextFresh                 // the detection is stale: move, or retry under the self mark, as the loop's budget table says; reads only
)

// Result is one attempt's outcome. Err is the public error: nil on Accept and Absent,
// except an accepted body over its cap or one that does not decode.
type Result struct {
	Err      error
	Verdict  Verdict
	Next     Next
	Sent     bool   // false when the send check refused the attempt's first request
	SelfMark uint64 // the epoch the attempt's own header-less answer stored at its admitted epoch; 0 otherwise
}

// Decide judges how one attempt ended, as transport selected it (an answer net/http
// returned or discarded after a malformed Location, a refused send, a transport error),
// against the Identity the request was sent on, before arrapi reads any body. An answer
// from another server version is never decoded. A success without the header is decoded
// only as a detection probe, whose status answer must still carry it; as data it is
// never decoded, and as a command's answer it is never success. So a Sonarr 5 answer to
// a request sent on a Sonarr 4 detection never becomes data.
func Decide(sent Identity, call Call, e End) (Verdict, Next) {
	read := call == CallRead || call == CallReadByID
	switch e.Kind {
	case EndNotSent:
		if read {
			return VerdictRefused, NextFresh
		}
		return VerdictRefused, NextDone
	case EndTransport:
		return VerdictTransport, decideTransport(call, read, e)
	case EndAnswer:
		return decideAnswer(sent, call, read, e)
	}
	return VerdictTransport, NextDone
}

func decideTransport(call Call, read bool, e End) Next {
	switch {
	case !e.Transient || call == CallMutation:
		return NextDone
	case e.HopFailed && read:
		return NextFresh
	case e.HopFailed:
		return NextDone
	}
	return NextRetry
}

func decideAnswer(sent Identity, call Call, read bool, e End) (Verdict, Next) {
	v, _, wellFormed := ParseAppVersion(e.Header)
	unanchoredProbe := call == CallProbe && sent.AppVersion == ""
	success := isSuccess(call, e.Code)
	transient := e.Code == http.StatusTooManyRequests || (e.Code >= 500 && e.Code <= 599)
	switch {
	case !wellFormed:
		return decideHeaderless(call, read, unanchoredProbe, success, transient)
	case v == sent.AppVersion || unanchoredProbe:
		return decideAttributed(call, success, transient, e.Code)
	case read:
		return VerdictChanged, NextFresh
	case call == CallMutation && success:
		return VerdictUnconfirmed, NextDone
	}
	return VerdictChanged, NextDone
}

// decideAttributed is rows 1 to 3: the answer carries the sent header.
func decideAttributed(call Call, success, transient bool, code int) (Verdict, Next) {
	switch {
	case success:
		return VerdictAccept, NextDone
	case call == CallReadByID && code == http.StatusNotFound:
		return VerdictAbsent, NextDone
	}
	return VerdictStatus, retryWhen(transient && call != CallMutation)
}

// decideHeaderless is rows 4 to 7: the answer carries no well-formed header.
// Sonarr 4, and Sonarr 5 before 5.0.0.2701, send exception-built errors without
// X-Application-Version (Sonarr#9161), and proxies can strip it, so this arm is permanent.
func decideHeaderless(call Call, read, unanchoredProbe, success, transient bool) (Verdict, Next) {
	switch {
	case success && call == CallProbe:
		return VerdictAccept, NextDone
	case success && call == CallMutation:
		return VerdictUnconfirmed, NextDone
	case success:
		return VerdictNoHeader, NextDone
	case unanchoredProbe:
		return VerdictStatus, retryWhen(transient)
	case read && transient:
		return VerdictUnidentified, NextFresh
	}
	return VerdictUnidentified, NextDone
}

func retryWhen(retry bool) Next {
	if retry {
		return NextRetry
	}
	return NextDone
}
