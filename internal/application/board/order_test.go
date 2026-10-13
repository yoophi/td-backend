package board

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

func TestMoveBeforeAcrossPositionedSubsetsAndHiddenCards(t *testing.T) {
	now := time.Now().UTC()
	issues := []domain.Issue{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	for mask := 0; mask < 16; mask++ {
		for _, hiddenKey := range []int{0, 10, math.MaxInt} {
			positions := []domain.BoardIssue{{BoardID: "board", IssueID: "hidden", Position: hiddenKey, AddedAt: now}}
			for n, i := range issues {
				if mask&(1<<n) != 0 {
					positions = append(positions, domain.BoardIssue{BoardID: "board", IssueID: i.ID, Position: 20 + n, AddedAt: now})
				}
			}
			for _, moved := range issues {
				for _, before := range []string{"a", "b", "c", "d", ""} {
					if before == moved.ID {
						continue
					}
					want := []string{}
					for _, v := range ApplyPositions("board", issues, positions) {
						if v.Issue.ID != moved.ID {
							want = append(want, v.Issue.ID)
						}
					}
					index := len(want)
					if before != "" {
						index = slices.Index(want, before)
					}
					want = slices.Insert(want, index, moved.ID)
					got, e := MoveBefore(positions, "board", issues, moved.ID, before, now.Add(time.Second))
					if e != nil {
						t.Fatal(mask, hiddenKey, moved.ID, before, e)
					}
					ids := []string{}
					for _, v := range ApplyPositions("board", issues, got) {
						ids = append(ids, v.Issue.ID)
					}
					if !reflect.DeepEqual(ids, want) {
						t.Fatal(mask, hiddenKey, moved.ID, before, ids, want, got)
					}
					seen := map[int]bool{}
					for _, p := range got {
						if seen[p.Position] {
							t.Fatal("duplicate key", got)
						}
						seen[p.Position] = true
						if p.IssueID == "hidden" && p != positions[0] {
							t.Fatal("hidden position changed", p, positions[0])
						}
					}
				}
			}
		}
	}
	for _, pair := range [][2]string{{"missing", "a"}, {"a", "missing"}, {"a", "a"}} {
		if _, e := MoveBefore(nil, "board", issues, pair[0], pair[1], now); !errors.Is(e, domain.ErrConflict) {
			t.Fatal(pair, e)
		}
	}
}

func TestInsertSlotOrderingAndExhaustedKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		keys  []int
		issue string
		slot  int
		want  []string
	}{
		{"empty", nil, "new", 1, []string{"new"}},
		{"top", []int{100, 200}, "new", 1, []string{"new", "a", "b"}},
		{"middle", []int{100, 200}, "new", 2, []string{"a", "new", "b"}},
		{"append clamps slot", []int{100, 200}, "new", 999, []string{"a", "b", "new"}},
		{"move removes original", []int{100, 200}, "a", 2, []string{"b", "a"}},
		{"gap exhausted", []int{100, 101}, "new", 2, []string{"a", "new", "b"}},
		{"underflow", []int{math.MinInt, 0}, "new", 1, []string{"new", "a", "b"}},
		{"overflow", []int{0, math.MaxInt}, "new", 3, []string{"a", "b", "new"}},
		{"wide midpoint", []int{math.MinInt, math.MaxInt}, "new", 2, []string{"a", "new", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			input := []domain.BoardIssue{}
			for i, key := range tc.keys {
				input = append(input, domain.BoardIssue{BoardID: "board", IssueID: string(rune('a' + i)), Position: key, AddedAt: now})
			}
			original := append([]domain.BoardIssue{}, input...)
			got, e := InsertSlot(input, "board", tc.issue, tc.slot, now)
			if e != nil {
				t.Fatal(e)
			}
			ids := []string{}
			for i, p := range got {
				ids = append(ids, p.IssueID)
				if i > 0 && got[i-1].Position >= p.Position {
					t.Fatal("not strictly ordered", got)
				}
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatal(ids, tc.want)
			}
			if !reflect.DeepEqual(input, original) {
				t.Fatal("mutated caller snapshot")
			}
		})
	}
}
