package main

import "testing"

func TestSessionStatusTerminal(t *testing.T) {
	cases := []struct {
		status SessionStatus
		want   bool
	}{
		{StatusClosed, true},
		{StatusTerminated, true},
		{StatusRunning, false},
		{StatusDisconnected, false},
		{StatusRecoveryGap, false},
		{StatusResumeError, false},
		{StatusResuming, false},
		{StatusStarting, false},
	}
	for _, c := range cases {
		if got := c.status.Terminal(); got != c.want {
			t.Errorf("%s.Terminal() = %v, want %v", c.status, got, c.want)
		}
	}
}

func TestSessionStatusCanResume(t *testing.T) {
	cases := []struct {
		status SessionStatus
		want   bool
	}{
		// Transport-level failures are recoverable.
		{StatusDisconnected, true},
		{StatusRecoveryGap, true},
		{StatusResumeError, true},
		// Nothing else is.
		{StatusRunning, false},
		{StatusStarting, false},
		{StatusResuming, false},
		{StatusClosed, false},
		{StatusTerminated, false},
	}
	for _, c := range cases {
		if got := c.status.CanResume(); got != c.want {
			t.Errorf("%s.CanResume() = %v, want %v", c.status, got, c.want)
		}
	}
}

// A status cannot be both final and recoverable.
func TestSessionStatusMutuallyExclusive(t *testing.T) {
	all := []SessionStatus{
		StatusStarting, StatusRunning, StatusDisconnected,
		StatusRecoveryGap, StatusResumeError, StatusResuming,
		StatusClosed, StatusTerminated,
	}
	for _, s := range all {
		if s.Terminal() && s.CanResume() {
			t.Errorf("%s is both Terminal and CanResume, which is contradictory", s)
		}
	}
}

// Every status must be non-empty and distinct (catches copy-paste duplicates).
func TestSessionStatusUnique(t *testing.T) {
	all := []SessionStatus{
		StatusStarting, StatusRunning, StatusDisconnected,
		StatusRecoveryGap, StatusResumeError, StatusResuming,
		StatusClosed, StatusTerminated,
	}
	seen := make(map[SessionStatus]bool, len(all))
	for _, s := range all {
		if s == "" {
			t.Error("found an empty status value")
		}
		if seen[s] {
			t.Errorf("duplicate status value: %q", s)
		}
		seen[s] = true
	}
}
