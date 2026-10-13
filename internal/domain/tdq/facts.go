package tdq

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

// Facts contains one project's already-loaded snapshot. Query evaluation never
// calls a database or performs per-issue I/O. Maps may omit empty collections.
type Facts struct {
	Issues       map[string]domain.Issue
	Logs         map[string][]domain.Log
	Comments     map[string][]domain.Comment
	Handoffs     map[string]*domain.Handoff
	Files        map[string][]domain.IssueFile
	Dependencies map[string][]string
	Rework       map[string]bool
}

func (f *Facts) GetIssue(id string) (*domain.Issue, error) {
	i, ok := f.Issues[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &i, nil
}
func (f *Facts) GetLogs(id string, _ int) ([]domain.Log, error)          { return f.Logs[id], nil }
func (f *Facts) GetComments(id string) ([]domain.Comment, error)         { return f.Comments[id], nil }
func (f *Facts) GetLatestHandoff(id string) (*domain.Handoff, error)     { return f.Handoffs[id], nil }
func (f *Facts) GetLinkedFiles(id string) ([]domain.IssueFile, error)    { return f.Files[id], nil }
func (f *Facts) GetDependencies(id string) ([]string, error)             { return f.Dependencies[id], nil }
func (f *Facts) GetRejectedInProgressIssueIDs() (map[string]bool, error) { return f.Rework, nil }
func (f *Facts) GetIssuesWithOpenDeps() (map[string]bool, error) {
	result := map[string]bool{}
	for id, deps := range f.Dependencies {
		for _, target := range deps {
			i, ok := f.Issues[target]
			if !ok || i.DeletedAt != nil || i.Status != domain.StatusClosed {
				result[id] = true
				break
			}
		}
	}
	return result, nil
}

// ParseIssues rejects standalone note predicates and prefix-only fields. The
// shared TDQ grammar also describes note queries, but boards select issues.
func ParseIssues(text string) (*Query, error) {
	q, e := Parse(text)
	if e != nil {
		return nil, e
	}
	if errs := q.Validate(); len(errs) > 0 {
		return nil, errs[0]
	}
	var validate func(Node) error
	fieldOK := func(field string) error {
		if field == "note" || strings.HasPrefix(field, "note.") || KnownFields[field] == "prefix" {
			return fmt.Errorf("%s is not an issue query field", field)
		}
		return nil
	}
	validate = func(n Node) error {
		switch v := n.(type) {
		case *BinaryExpr:
			if e := validate(v.Left); e != nil {
				return e
			}
			return validate(v.Right)
		case *UnaryExpr:
			return validate(v.Expr)
		case *FieldExpr:
			return fieldOK(v.Field)
		case *FunctionCall:
			if v.Name == "has" || v.Name == "any" || v.Name == "all" || v.Name == "none" {
				field := fmt.Sprint(v.Args[0])
				// Collection functions operate on scalar Issue fields. Cross-entity
				// existence/negation uses its explicit predicate in the AST.
				if e := fieldOK(field); e != nil {
					return e
				}
				if strings.Contains(field, ".") || NewEvaluator(&EvalContext{}, &Query{}).getFieldGetter(field) == nil {
					return fmt.Errorf("%s() requires a scalar issue field", v.Name)
				}
			}
		}
		return nil
	}
	if e = validate(q.Root); e != nil {
		return nil, e
	}
	return q, nil
}

// Filter evaluates the full Boolean expression before sorting. It does not
// truncate the input scan or silently return a partial board.
type FilterOptions struct{ IncludeClosed, WithDeleted bool }

func Filter(query string, facts *Facts, ctx EvalContext, includeClosed bool) ([]domain.Issue, error) {
	return Select(query, facts, ctx, FilterOptions{IncludeClosed: includeClosed})
}

// Select evaluates the same snapshot while applying the caller's visibility.
func Select(query string, facts *Facts, ctx EvalContext, opts FilterOptions) ([]domain.Issue, error) {
	q, e := ParseIssues(query)
	if e != nil {
		return nil, e
	}
	return SelectQuery(q, facts, ctx, opts)
}

// SelectQuery evaluates an already validated Issue query. This lets a caller
// apply an explicit sort override without rewriting or concatenating input.
func SelectQuery(q *Query, facts *Facts, ctx EvalContext, opts FilterOptions) ([]domain.Issue, error) {
	issues := []domain.Issue{}
	for _, i := range facts.Issues {
		if (opts.WithDeleted || i.DeletedAt == nil) && (opts.IncludeClosed || i.Status != domain.StatusClosed) {
			issues = append(issues, i)
		}
	}
	result, e := applyCrossEntityFilters(facts, issues, q, &ctx)
	if e != nil {
		return nil, e
	}
	if result == nil {
		result = []domain.Issue{}
	}
	field, desc := "priority", false
	if q.Sort != nil {
		field, desc = q.Sort.Field, q.Sort.Descending
	}
	slices.SortFunc(result, func(a, b domain.Issue) int {
		v := compareSort(sortValue(a, field), sortValue(b, field))
		if desc {
			v = -v
		}
		if v == 0 {
			v = cmp.Compare(a.ID, b.ID)
		}
		return v
	})
	return result, nil
}
func sortValue(i domain.Issue, field string) any {
	switch field {
	case "id":
		return i.ID
	case "title":
		return i.Title
	case "priority":
		return string(i.Priority)
	case "status":
		return string(i.Status)
	case "points":
		return i.Points
	case "sprint":
		return i.Sprint
	case "defer_count":
		return i.DeferCount
	case "created_at":
		return i.CreatedAt
	case "updated_at":
		return i.UpdatedAt
	case "closed_at":
		if i.ClosedAt != nil {
			return *i.ClosedAt
		}
	case "deleted_at":
		if i.DeletedAt != nil {
			return *i.DeletedAt
		}
	case "due_date":
		if i.DueDate != nil {
			return *i.DueDate
		}
	case "defer_until":
		if i.DeferUntil != nil {
			return *i.DeferUntil
		}
	}
	return nil
}
func compareSort(a, b any) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return -1
	}
	if b == nil {
		return 1
	}
	switch v := a.(type) {
	case string:
		return cmp.Compare(v, b.(string))
	case int:
		return cmp.Compare(v, b.(int))
	case time.Time:
		return v.Compare(b.(time.Time))
	}
	return 0
}
