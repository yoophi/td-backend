package tdq

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

func TestSnapshotFactsBooleanRelationsAndDates(t *testing.T) {
	now := time.Date(2026, 10, 13, 14, 0, 0, 0, time.UTC)
	f := Facts{
		Issues: map[string]domain.Issue{
			"epic": {ID: "epic", Type: domain.TypeEpic, Title: "Release", Priority: domain.PriorityP1, Status: domain.StatusOpen},
			"a":    {ID: "a", ParentID: "epic", Title: "Alpha", Priority: domain.PriorityP2, Status: domain.StatusOpen, ImplementerSession: "me", CreatedAt: now},
			"b":    {ID: "b", ParentID: "a", Title: "Beta", Priority: domain.PriorityP2, Status: domain.StatusInProgress, CreatedAt: now.AddDate(0, 0, -1)},
			"c":    {ID: "c", Title: "Closed", Priority: domain.PriorityP0, Status: domain.StatusClosed},
			"d":    {ID: "d", Title: "Deleted", Priority: domain.PriorityP0, Status: domain.StatusOpen, DeletedAt: &now},
		},
		Dependencies: map[string][]string{"a": {"b"}, "b": {"c"}},
		Logs:         map[string][]domain.Log{"a": {{Message: "fix security", SessionID: "me", Timestamp: now}}, "b": {{Message: "documentation", SessionID: "other", Timestamp: now.AddDate(0, 0, -1)}}},
		Comments:     map[string][]domain.Comment{"b": {{Text: "reviewed", SessionID: "me", CreatedAt: now}}},
		Handoffs:     map[string]*domain.Handoff{"b": {Done: []string{"implementation complete"}, Timestamp: now}},
		Files:        map[string][]domain.IssueFile{"a": {{FilePath: "src/main.go", Role: domain.FileRoleImplementation}}},
		Rework:       map[string]bool{"b": true},
	}
	ctx := EvalContext{CurrentSession: "me", Now: now}
	for _, tc := range []struct {
		query  string
		closed bool
		want   []string
	}{
		{"", false, []string{"epic", "a", "b"}},
		{"", true, []string{"c", "epic", "a", "b"}},
		{`log.message ~ security OR title = Beta`, false, []string{"a", "b"}},
		{`NOT (log.message ~ security OR title = Beta)`, false, []string{"epic"}},
		{`comment.session = @me AND handoff.done ~ complete`, false, []string{"b"}},
		{`log.timestamp = today`, false, []string{"a"}},
		{`log.timestamp != today`, false, []string{"b"}},
		{`comment.created = today`, false, []string{"b"}},
		{`handoff.timestamp = today`, false, []string{"b"}},
		{`created > 2026-10-12`, false, []string{"a"}},
		{`epic = epic AND NOT child_of(epic)`, false, []string{"b"}},
		{`descendant_of(epic)`, false, []string{"a", "b"}},
		{`blocks(a)`, false, []string{"b"}},
		{`blocked_by(b)`, false, []string{"a"}},
		{`dep.blocks = a`, false, []string{"b"}},
		{`dep.depends_on = b`, false, []string{"a"}},
		{`has_open_deps()`, false, []string{"a"}},
		{`is_ready() AND rework()`, false, []string{"b"}},
		{`linked_to("src/") AND file.role = implementation`, false, []string{"a"}},
		{`implementer = @me`, false, []string{"a"}},
		{`priority = P2 sort:-title`, false, []string{"b", "a"}},
		{`priority = P2 sort:-priority`, false, []string{"a", "b"}},
		{`status = closed`, false, []string{}},
		{`status = closed`, true, []string{"c"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			issues, e := Filter(tc.query, &f, ctx, tc.closed)
			if e != nil {
				t.Fatal(e)
			}
			ids := []string{}
			for _, i := range issues {
				ids = append(ids, i.ID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatal(ids, tc.want)
			}
		})
	}
	// Missing/deleted prerequisites remain unresolved rather than becoming ready.
	f.Dependencies["a"] = []string{"missing"}
	f.Dependencies["b"] = []string{"d"}
	blocked, _ := f.GetIssuesWithOpenDeps()
	if !blocked["a"] || !blocked["b"] {
		t.Fatal(blocked)
	}
}

func TestIssueQueryRejectsUnrelatedOrUnsupportedFields(t *testing.T) {
	for _, query := range []string{`note.title ~ x`, `log = x`, `has(note)`, `has(epic)`, `any(log.message,x)`, `has(unknown)`, `unknown = x`, `log.timestamp > someday`, `log.timestamp > today`, `sort:unknown`} {
		if _, e := ParseIssues(query); e == nil {
			t.Errorf("accepted %q", query)
		}
	}
}

func TestFilterDoesNotTruncateBeforeMatching(t *testing.T) {
	f := Facts{Issues: map[string]domain.Issue{}}
	for n := 0; n < 10010; n++ {
		id := fmt.Sprintf("issue-%05d", n)
		f.Issues[id] = domain.Issue{ID: id, Status: domain.StatusOpen, Priority: domain.PriorityP2}
	}
	got, e := Filter(`id ~ "issue-100" sort:id`, &f, EvalContext{Now: time.Now()}, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 10 || got[0].ID != "issue-10000" || got[9].ID != "issue-10009" {
		t.Fatal("incomplete snapshot filtering", len(got))
	}
}

func TestDateGranularityAndStableNullableSort(t *testing.T) {
	zone := time.FixedZone("PDT", -7*60*60)
	now := time.Date(2026, 5, 9, 23, 59, 0, 0, zone)
	due := "2026-05-10"
	f := Facts{Issues: map[string]domain.Issue{
		"a": {ID: "a", Status: domain.StatusOpen, CreatedAt: now, DueDate: &due},
		"b": {ID: "b", Status: domain.StatusOpen, CreatedAt: now.Add(time.Minute)},
		"c": {ID: "c", Status: domain.StatusOpen, CreatedAt: now.Add(-time.Hour), DueDate: &due},
	}}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{`created = 2026-05-09 sort:id`, []string{"a", "c"}},
		{`created > 2026-05-09 sort:id`, []string{"b"}},
		{`created >= -1h sort:created`, []string{"c", "a", "b"}},
		{`due = NULL`, []string{"b"}},
		{`sort:due`, []string{"b", "a", "c"}},
		{`sort:-due`, []string{"a", "c", "b"}},
	} {
		got, e := Filter(tc.query, &f, EvalContext{Now: now}, false)
		if e != nil {
			t.Fatal(tc.query, e)
		}
		ids := []string{}
		for _, i := range got {
			ids = append(ids, i.ID)
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Fatal(tc.query, ids, tc.want)
		}
	}
}
