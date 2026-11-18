// Package stages defines the application pipeline: the statuses an application can
// be in and which moves between them are allowed (a small state machine).
//
//	from          may move to
//	wishlist      applied, withdrawn
//	applied       screening, interviewing, offer, rejected, withdrawn
//	screening     interviewing, offer, rejected, withdrawn
//	interviewing  interviewing (another round), offer, rejected, withdrawn
//	offer         accepted, rejected, withdrawn
//	accepted      withdrawn
//	rejected      - (terminal)
//	withdrawn     - (terminal)
//
// To try a company again after a terminal status, create a new application.
package stages

import "slices"

// Statuses in pipeline order. Must match the CHECK constraints in the migrations.
const (
	Wishlist     = "wishlist"
	Applied      = "applied"
	Screening    = "screening"
	Interviewing = "interviewing"
	Offer        = "offer"
	Accepted     = "accepted"
	Rejected     = "rejected"
	Withdrawn    = "withdrawn"
)

// All lists every status in pipeline order.
var All = []string{Wishlist, Applied, Screening, Interviewing, Offer, Accepted, Rejected, Withdrawn}

// Pipeline is the forward path an application moves along, in order: what the stats
// funnel counts. wishlist comes before it (not yet applied); rejected and withdrawn end
// it.
var Pipeline = []string{Applied, Screening, Interviewing, Offer, Accepted}

// transitions maps each status to the statuses it may move to, in pipeline order.
var transitions = map[string][]string{
	Wishlist:     {Applied, Withdrawn},
	Applied:      {Screening, Interviewing, Offer, Rejected, Withdrawn},
	Screening:    {Interviewing, Offer, Rejected, Withdrawn},
	Interviewing: {Interviewing, Offer, Rejected, Withdrawn}, // repeat = another interview round
	Offer:        {Accepted, Rejected, Withdrawn},
	Accepted:     {Withdrawn}, // e.g. reneged before the start date
	Rejected:     {},
	Withdrawn:    {},
}

// Valid reports whether s is a known status.
func Valid(s string) bool {
	_, ok := transitions[s]
	return ok
}

// CanTransition reports whether an application may move from one status to another.
func CanTransition(from, to string) bool {
	return slices.Contains(transitions[from], to)
}

// Next returns the statuses reachable from s (empty for terminal or unknown statuses).
// The result is a copy, so callers may modify it.
func Next(s string) []string {
	return slices.Clone(transitions[s])
}

// Terminal reports whether s is a final status, with no moves out of it.
func Terminal(s string) bool {
	return Valid(s) && len(transitions[s]) == 0
}
