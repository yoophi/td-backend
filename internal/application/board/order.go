package board

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

const PositionGap = 65536

func ApplyPositions(id string, issues []domain.Issue, positions []domain.BoardIssue) []domain.BoardIssueView {
	keys := map[string]int{}
	for _, p := range positions {
		keys[p.IssueID] = p.Position
	}
	result := make([]domain.BoardIssueView, 0, len(issues))
	for _, i := range issues {
		key, ok := keys[i.ID]
		result = append(result, domain.BoardIssueView{BoardID: id, Issue: i, Position: key, HasPosition: ok})
	}
	// Keep query order for unpositioned cards. Category is deliberately the
	// same empty value as td serve; monitor classification is a separate use case.
	slices.SortStableFunc(result, func(a, b domain.BoardIssueView) int {
		if a.HasPosition != b.HasPosition {
			if a.HasPosition {
				return -1
			}
			return 1
		}
		if a.HasPosition {
			return cmp.Compare(a.Position, b.Position)
		}
		return 0
	})
	return result
}

// MoveBefore materializes only the positioned prefix needed for visible order.
// Hidden cards keep both their keys and timestamps. Unpositioned cards below
// the moved card continue to follow the board query.
func MoveBefore(existing []domain.BoardIssue, boardID string, issues []domain.Issue, issueID, beforeID string, now time.Time) ([]domain.BoardIssue, error) {
	views := ApplyPositions(boardID, issues, existing)
	old := map[string]domain.BoardIssue{}
	maxKey := 0
	for _, p := range existing {
		old[p.IssueID] = p
		maxKey = max(maxKey, p.Position)
	}
	order := []string{}
	found := false
	for _, v := range views {
		if v.Issue.ID == issueID {
			found = true
		} else {
			order = append(order, v.Issue.ID)
		}
	}
	index := len(order)
	if beforeID != "" {
		index = slices.Index(order, beforeID)
	}
	if !found || index < 0 {
		return nil, domain.ErrConflict
	}
	order = slices.Insert(order, index, issueID)
	prefixLen := index + 1
	for n, id := range order {
		if _, ok := old[id]; ok {
			prefixLen = max(prefixLen, n+1)
		}
	}
	prefix := []domain.BoardIssue{}
	respace := false
	for _, id := range order[:prefixLen] {
		if id == issueID {
			continue
		}
		p, ok := old[id]
		if !ok {
			if maxKey > math.MaxInt-PositionGap {
				respace = true
			} else {
				maxKey += PositionGap
			}
			p = domain.BoardIssue{BoardID: boardID, IssueID: id, Position: maxKey, AddedAt: now}
		}
		prefix = append(prefix, p)
	}
	key, ok := insertionKey(prefix, index)
	respace = respace || !ok
	prefix = slices.Insert(prefix, index, domain.BoardIssue{BoardID: boardID, IssueID: issueID, Position: key, AddedAt: now})
	visible := map[string]bool{}
	for _, p := range prefix {
		visible[p.IssueID] = true
	}
	hidden := []domain.BoardIssue{}
	occupied := map[int]bool{}
	for _, p := range existing {
		if !visible[p.IssueID] {
			hidden = append(hidden, p)
			occupied[p.Position] = true
		}
	}
	seen := map[int]bool{}
	for _, p := range prefix {
		if occupied[p.Position] || seen[p.Position] {
			respace = true
		}
		seen[p.Position] = true
	}
	if respace {
		// Prefer keys above all existing rows, as td does. If that would
		// overflow, select ascending free sparse keys while retaining hidden rows.
		next := maxKey
		if len(prefix) > math.MaxInt/PositionGap || maxKey > math.MaxInt-len(prefix)*PositionGap {
			next = 0
		}
		for n := range prefix {
			for {
				if next > math.MaxInt-PositionGap {
					return nil, domain.ErrConflict
				}
				next += PositionGap
				if !occupied[next] {
					break
				}
			}
			prefix[n].Position = next
			prefix[n].AddedAt = now
		}
	}
	result := append(hidden, prefix...)
	slices.SortFunc(result, func(a, b domain.BoardIssue) int { return cmp.Compare(a.Position, b.Position) })
	return result, nil
}

// InsertSlot interprets slot as a 1-based index after removing the moved card.
// Sparse keys and existing timestamps survive unless spacing is exhausted.
func InsertSlot(existing []domain.BoardIssue, boardID, issueID string, slot int, now time.Time) ([]domain.BoardIssue, error) {
	if slot < 1 {
		return nil, invalid("position", "expected positive insertion slot")
	}
	result := make([]domain.BoardIssue, 0, len(existing)+1)
	for _, p := range existing {
		if p.IssueID != issueID {
			result = append(result, p)
		}
	}
	slices.SortFunc(result, func(a, b domain.BoardIssue) int {
		if a.Position < b.Position {
			return -1
		}
		if a.Position > b.Position {
			return 1
		}
		return 0
	})
	index := min(slot-1, len(result))
	key, ok := insertionKey(result, index)
	if !ok {
		if len(result) >= math.MaxInt/PositionGap-1 {
			return nil, domain.ErrConflict
		}
		for n := range result {
			result[n].Position = (n + 1) * PositionGap
		}
		key, ok = insertionKey(result, index)
		if !ok {
			return nil, domain.ErrConflict
		}
	}
	return slices.Insert(result, index, domain.BoardIssue{BoardID: boardID, IssueID: issueID, Position: key, AddedAt: now}), nil
}
func insertionKey(positions []domain.BoardIssue, index int) (int, bool) {
	if len(positions) == 0 {
		return PositionGap, true
	}
	if index == 0 {
		lo := positions[0].Position
		if lo < math.MinInt+PositionGap {
			return 0, false
		}
		return lo - PositionGap, true
	}
	if index == len(positions) {
		hi := positions[index-1].Position
		if hi > math.MaxInt-PositionGap {
			return 0, false
		}
		return hi + PositionGap, true
	}
	lo, hi := positions[index-1].Position, positions[index].Position
	// Bitwise midpoint avoids signed overflow for widely separated keys.
	mid := (lo & hi) + ((lo ^ hi) >> 1)
	return mid, lo < mid && mid < hi
}
