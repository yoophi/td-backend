package domain

import "time"

type Project struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Repository       string    `json:"repository,omitempty"`
	ReviewPolicyMode string    `json:"review_policy_mode"`
	TitleMinLength   int       `json:"title_min_length"`
	TitleMaxLength   int       `json:"title_max_length"`
	ChangeSeq        int64     `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	Capabilities     []string  `json:"capabilities"`
}

type Actor struct {
	ID               string
	Name             string
	CanCreateProject bool
}

type Session struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Branch            string    `json:"branch"`
	AgentType         string    `json:"agent_type"`
	AgentPID          int       `json:"agent_pid"`
	ContextID         string    `json:"context_id"`
	PreviousSessionID *string   `json:"previous_session_id"`
	StartedAt         time.Time `json:"started_at"`
	LastActivity      time.Time `json:"last_activity"`
	ActorID           string    `json:"-"`
}
