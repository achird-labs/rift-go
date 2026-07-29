package rift

import (
	"errors"
	"fmt"
)

// Sentinel errors. Callers match with errors.Is; every error the SDK returns wraps one of
// these so a caller can branch on kind without string matching.
var (
	// ErrEngineUnavailable means the engine could not be reached, started, or loaded:
	// a refused admin connection, a spawn failure, a missing native library.
	ErrEngineUnavailable = errors.New("rift: engine unavailable")

	// ErrInvalidDefinition means the SDK rejected a definition before it reached the engine,
	// or could not encode/decode one.
	ErrInvalidDefinition = errors.New("rift: invalid definition")

	// ErrImposterNotFound means the addressed imposter does not exist on the engine.
	ErrImposterNotFound = errors.New("rift: imposter not found")

	// ErrVersionMismatch means the loaded native library reports a C-ABI version this SDK
	// does not support.
	ErrVersionMismatch = errors.New("rift: engine ABI version mismatch")

	// ErrClosed means the engine or handle has already been stopped.
	ErrClosed = errors.New("rift: engine closed")

	// ErrVerificationFailed means a verification's match count did not meet expectations.
	// The concrete error is a *VerificationError carrying the engine's answer.
	ErrVerificationFailed = errors.New("rift: verification failed")
)

// EngineError is a failure reported by the engine itself, carrying whatever detail the
// transport surfaced: an HTTP status for the admin API, or the rift_last_error string for
// the embedded C ABI.
type EngineError struct {
	// Code is the HTTP status for remote transports, or 0 when the engine reported only a
	// message (the embedded lane).
	Code int
	// Message is the engine's own description of the failure.
	Message string
	// Op names the SDK operation that failed, e.g. "create imposter".
	Op string

	kind error
}

func (e *EngineError) Error() string {
	switch {
	case e.Op != "" && e.Code != 0:
		return fmt.Sprintf("rift: %s failed (%d): %s", e.Op, e.Code, e.Message)
	case e.Op != "":
		return fmt.Sprintf("rift: %s failed: %s", e.Op, e.Message)
	case e.Code != 0:
		return fmt.Sprintf("rift: engine error (%d): %s", e.Code, e.Message)
	default:
		return "rift: engine error: " + e.Message
	}
}

// Unwrap reports the sentinel this failure belongs to, so errors.Is(err, ErrImposterNotFound)
// works on an EngineError produced by any transport.
func (e *EngineError) Unwrap() error { return e.kind }

// NewEngineError builds an EngineError attributed to a sentinel kind, so errors.Is works across
// it. Transports use this to report engine-side failures uniformly; kind may be nil when the
// failure does not map onto a sentinel.
func NewEngineError(op, message string, code int, kind error) *EngineError {
	return &EngineError{Code: code, Message: message, Op: op, kind: kind}
}

// wrapInvalid annotates a definition-level failure with the operation that produced it.
func wrapInvalid(op string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrInvalidDefinition, op, err)
}

// wrapUnavailable annotates a transport-level failure — the engine was never reached.
func wrapUnavailable(op string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrEngineUnavailable, op, err)
}

// newEngineError builds an EngineError classified against a sentinel. An HTTP 404 becomes
// ErrImposterNotFound; everything else defaults to ErrInvalidDefinition, on the reasoning that
// a request the engine understood but rejected is a definition problem, whereas a request that
// never arrived is surfaced as ErrEngineUnavailable by the transport layer instead.
func newEngineError(op string, code int, msg string) *EngineError {
	kind := ErrInvalidDefinition
	switch code {
	case 404:
		kind = ErrImposterNotFound
	case 502, 503, 504:
		kind = ErrEngineUnavailable
	}
	return &EngineError{Code: code, Message: msg, Op: op, kind: kind}
}
