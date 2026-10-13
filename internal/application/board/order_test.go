package board

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

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
