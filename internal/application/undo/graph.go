package undo

import (
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

// An inverse may restore an edge which now closes a cycle via another issue's
// later changes. Validate the final graph, including all cascade inverses, before
// commit. A conflict rolls back every inverse and its audit together.
func validateGraph(tx port.TxScope, c domain.Caller, effects []port.Effect) error {
	hasIssues := false
	for _, e := range effects {
		if e.EntityKind == "issue" {
			hasIssues = true
			break
		}
	}
	if !hasIssues {
		return nil
	}
	facts, e := tx.Queries().Facts(tx.Context(), c.ProjectID, true)
	if e != nil {
		return e
	}
	parents := map[string][]string{}
	deps := map[string][]string{}
	for id, i := range facts.Issues {
		if i.DeletedAt != nil {
			continue
		}
		parents[id] = []string{}
		deps[id] = facts.Dependencies[id]
		if i.ParentID != "" {
			parents[id] = []string{i.ParentID}
		}
	}
	for _, graph := range []map[string][]string{parents, deps} {
		color := map[string]int{}
		var visit func(string) bool
		visit = func(id string) bool {
			if color[id] == 1 {
				return false
			}
			if color[id] == 2 {
				return true
			}
			edges, ok := graph[id]
			if !ok {
				return true
			}
			color[id] = 1
			for _, target := range edges {
				if !visit(target) {
					return false
				}
			}
			color[id] = 2
			return true
		}
		for id := range graph {
			if !visit(id) {
				return domain.ErrConflict
			}
		}
	}
	return nil
}
