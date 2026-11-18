package stages

import (
	"slices"
	"testing"
)

func TestEveryStatusHasTransitions(t *testing.T) {
	if len(transitions) != len(All) {
		t.Fatalf("transitions has %d entries, All has %d", len(transitions), len(All))
	}
	for _, s := range All {
		if !Valid(s) {
			t.Errorf("%q is in All but not Valid", s)
		}
		for _, to := range transitions[s] {
			if !Valid(to) {
				t.Errorf("%s -> %q: target is not a valid status", s, to)
			}
		}
	}
	if Valid("hired") || Valid("") {
		t.Error("unknown statuses must not be Valid")
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to string
		want     bool
	}{
		{Wishlist, Applied, true},
		{Applied, Screening, true},
		{Applied, Interviewing, true}, // some companies skip the screen
		{Screening, Interviewing, true},
		{Interviewing, Interviewing, true}, // another round
		{Interviewing, Offer, true},
		{Offer, Accepted, true},
		{Accepted, Withdrawn, true},
		{Applied, Rejected, true},
		{Offer, Withdrawn, true},

		{Applied, Applied, false}, // no-op moves are not events
		{Applied, Wishlist, false},
		{Interviewing, Screening, false},
		{Wishlist, Offer, false},
		{Applied, Accepted, false},
		{Rejected, Offer, false},
		{Withdrawn, Applied, false},
		{Accepted, Rejected, false},
		{"hired", Applied, false},
		{Applied, "hired", false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestTerminalStatuses(t *testing.T) {
	for _, s := range []string{Rejected, Withdrawn} {
		if n := Next(s); len(n) != 0 {
			t.Errorf("Next(%s) = %v, want none (terminal)", s, n)
		}
	}
}

func TestNextReturnsCopy(t *testing.T) {
	n := Next(Applied)
	n[0] = "mutated"
	if !slices.Contains(Next(Applied), Screening) {
		t.Error("modifying Next's result changed the state machine")
	}
}

// Every non-terminal status must be able to reach a terminal one, so no application
// can get stuck.
func TestEveryStatusCanFinish(t *testing.T) {
	terminal := map[string]bool{Rejected: true, Withdrawn: true}
	for _, start := range All {
		seen := map[string]bool{start: true}
		queue := []string{start}
		reached := terminal[start]
		for len(queue) > 0 && !reached {
			cur := queue[0]
			queue = queue[1:]
			for _, nxt := range transitions[cur] {
				if terminal[nxt] {
					reached = true
				}
				if !seen[nxt] {
					seen[nxt] = true
					queue = append(queue, nxt)
				}
			}
		}
		if !reached {
			t.Errorf("%s cannot reach a terminal status", start)
		}
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range All {
		want := s == Rejected || s == Withdrawn
		if got := Terminal(s); got != want {
			t.Errorf("Terminal(%q) = %v, want %v", s, got, want)
		}
	}
	if Terminal("nope") {
		t.Error(`Terminal("nope") = true, want false`)
	}
}

func TestPipelineIsInStatusOrder(t *testing.T) {
	// The funnel relies on Pipeline being the forward path, in the order of All.
	last := -1
	for _, s := range Pipeline {
		i := slices.Index(All, s)
		if i <= last || Terminal(s) {
			t.Errorf("%q is out of order or final in Pipeline %v", s, Pipeline)
		}
		last = i
	}
}
