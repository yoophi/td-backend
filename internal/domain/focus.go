package domain

// SessionFocus is shared only within a project and owning Session. Revision zero
// represents a focus that has never been stored; a cleared stored focus retains
// its revision for conditional undo.
type SessionFocus struct {
	SessionID string  `json:"session_id"`
	IssueID   *string `json:"issue_id"`
	Revision  int64   `json:"revision_number"`
}
