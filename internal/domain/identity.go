package domain

import "regexp"

var projectIDPattern = regexp.MustCompile(`^prj_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var operationKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func ValidProjectID(id string) bool     { return projectIDPattern.MatchString(id) }
func ValidOperationKey(key string) bool { return operationKeyPattern.MatchString(key) }

type Role string

const (
	RoleReader Role = "reader"
	RoleWriter Role = "writer"
	RoleOwner  Role = "owner"
)

func (r Role) CanWrite() bool { return r == RoleWriter || r == RoleOwner }

// Caller identity comes from authentication, not JSON fields supplied by clients.
type Caller struct {
	Actor                Actor
	ProjectID, SessionID string
}
