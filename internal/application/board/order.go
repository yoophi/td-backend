package board

import (
	"math"
	"slices"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

const PositionGap = 65536

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
