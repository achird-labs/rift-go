package rift

import "fmt"

// Verification asks the engine how many recorded requests match a predicate set. The counting
// happens engine-side, through the same predicate evaluator that serves requests, so xpath,
// jsonpath and inject predicates work identically in a verification and in a stub — which they
// would not if the SDK reimplemented matching over a downloaded journal.

// VerifyRequest is the body of a verification.
type VerifyRequest struct {
	Predicates []Predicate `json:"predicates,omitempty"`
	// FlowID scopes verification to one flow's space.
	FlowID string `json:"flowId,omitempty"`
	// IncludeRequests returns the matching requests, not just the count.
	IncludeRequests bool `json:"includeRequests,omitempty"`
	// IncludeClosest returns the nearest non-matching request, which is what makes a failed
	// assertion diagnosable rather than merely red.
	IncludeClosest bool `json:"includeClosest,omitempty"`
}

// VerifyResult is the engine's answer.
type VerifyResult struct {
	Matched int `json:"matched"`
	Total   int `json:"total"`
	// Requests is populated when IncludeRequests was set.
	Requests []RecordedRequest `json:"requests,omitempty"`
	// Closest is populated when IncludeClosest was set and nothing matched.
	Closest []RecordedRequest `json:"closest,omitempty"`
}

// CountMatcher constrains how many matches a verification expects.
type CountMatcher struct {
	min  int
	max  int // -1 means unbounded
	desc string
}

// Times expects exactly n matches.
func Times(n int) CountMatcher {
	return CountMatcher{min: n, max: n, desc: fmt.Sprintf("exactly %d", n)}
}

// Once expects exactly one match.
func Once() CountMatcher { return CountMatcher{min: 1, max: 1, desc: "exactly 1"} }

// Never expects no matches.
func Never() CountMatcher { return CountMatcher{min: 0, max: 0, desc: "never"} }

// AtLeast expects n or more matches.
func AtLeast(n int) CountMatcher {
	return CountMatcher{min: n, max: -1, desc: fmt.Sprintf("at least %d", n)}
}

// AtMost expects n or fewer matches.
func AtMost(n int) CountMatcher {
	return CountMatcher{min: 0, max: n, desc: fmt.Sprintf("at most %d", n)}
}

// Between expects a match count in [lo, hi].
func Between(lo, hi int) CountMatcher {
	return CountMatcher{min: lo, max: hi, desc: fmt.Sprintf("between %d and %d", lo, hi)}
}

// Satisfied reports whether n matches meet the expectation.
func (c CountMatcher) Satisfied(n int) bool {
	if n < c.min {
		return false
	}
	return c.max < 0 || n <= c.max
}

// String describes the expectation, for failure messages.
func (c CountMatcher) String() string {
	if c.desc == "" {
		return "any number of"
	}
	return c.desc
}

// VerificationError reports a verification whose count did not meet expectations. It carries the
// engine's answer so a test framework can render the near-miss rather than just "want 1 got 0".
type VerificationError struct {
	Expected CountMatcher
	Result   VerifyResult
	// Predicates is what was asked for, so the message can show both sides.
	Predicates []Predicate
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("rift: verification failed: expected %s matching request(s), got %d of %d recorded",
		e.Expected, e.Result.Matched, e.Result.Total)
}

// Unwrap classifies verification failures so callers can match on them generically.
func (e *VerificationError) Unwrap() error { return ErrVerificationFailed }
