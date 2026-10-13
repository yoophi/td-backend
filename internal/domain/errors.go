package domain

import "errors"

var (
	ErrNotFound     = errors.New("not found")
	ErrStale        = errors.New("stale revision")
	ErrConflict     = errors.New("conflict")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrUnavailable  = errors.New("store unavailable")
	ErrScopeClosed  = errors.New("transaction scope is closed")
)

// ValidationError is transport independent. HTTP adapters preserve its field errors.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string { return "validation failed" }
