package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// Revision is opaque to clients. Its numeric representation remains internal.
func Revision(n int64) string { return fmt.Sprintf("r-%d", n) }

// Observe accepts exactly one strong aggregate ETag. Wildcards, weak validators
// and validator lists cannot establish which state the caller actually observed.
func Observe(value string) (int64, error) {
	if value == "" {
		return 0, ErrPreconditionRequired
	}
	invalid := func() (int64, error) {
		return 0, &ValidationError{Fields: []FieldError{{Field: "If-Match", Rule: "invalid", Message: "one strong quoted aggregate revision required"}}}
	}
	if !strings.HasPrefix(value, `"r-`) || !strings.HasSuffix(value, `"`) {
		return invalid()
	}
	text := value[3 : len(value)-1]
	n, e := strconv.ParseInt(text, 10, 64)
	if e != nil || n < 1 || strconv.FormatInt(n, 10) != text {
		return invalid()
	}
	return n, nil
}
