package tdq

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	models "github.com/yoophi/td-backend/internal/domain"
)

// EvalContext provides context for query evaluation
type EvalContext struct {
	CurrentSession string    // for @me resolution
	Now            time.Time // for relative date calculation
}

// NewEvalContext creates a new evaluation context
func NewEvalContext(sessionID string) *EvalContext {
	return &EvalContext{
		CurrentSession: sessionID,
		Now:            time.Now(),
	}
}

// Evaluator evaluates a Query AST against typed in-memory values
type Evaluator struct {
	ctx   *EvalContext
	query *Query
}

// NewEvaluator creates a new query evaluator
func NewEvaluator(ctx *EvalContext, query *Query) *Evaluator {
	return &Evaluator{ctx: ctx, query: query}
}

// ToMatcher returns a function that matches issues in memory
// Cross-entity predicates must use Filter so related facts are evaluated.
func (e *Evaluator) ToMatcher() (func(models.Issue) bool, error) {
	if e.query.Root == nil {
		return func(models.Issue) bool { return true }, nil
	}
	return e.nodeToMatcher(e.query.Root)
}

// HasCrossEntityConditions checks if the query has cross-entity conditions
func (e *Evaluator) HasCrossEntityConditions() bool {
	if e.query.Root == nil {
		return false
	}
	return e.hasCrossEntity(e.query.Root)
}

func (e *Evaluator) hasCrossEntity(n Node) bool {
	switch node := n.(type) {
	case *BinaryExpr:
		return e.hasCrossEntity(node.Left) || e.hasCrossEntity(node.Right)
	case *UnaryExpr:
		return e.hasCrossEntity(node.Expr)
	case *FieldExpr:
		// "epic" without dot (e.g., "epic = td-123") requires walking the parent chain
		if node.Field == "epic" {
			return true
		}
		parts := strings.Split(node.Field, ".")
		if len(parts) > 1 {
			prefix := parts[0]
			return prefix == "log" || prefix == "comment" || prefix == "handoff" || prefix == "file" || prefix == "dep" || prefix == "epic"
		}
		return false
	case *FunctionCall:
		return node.Name == "blocks" || node.Name == "blocked_by" || node.Name == "linked_to" || node.Name == "descendant_of" || node.Name == "rework" || node.Name == "is_ready" || node.Name == "has_open_deps"
	default:
		return false
	}
}

// isCrossEntityNode checks if a node is a cross-entity field expression
// Used to skip in-memory negation for cross-entity conditions
func (e *Evaluator) isCrossEntityNode(n Node) bool {
	switch node := n.(type) {
	case *FieldExpr:
		// "epic" without dot (e.g., "epic = td-123") requires walking the parent chain
		if node.Field == "epic" {
			return true
		}
		parts := strings.Split(node.Field, ".")
		if len(parts) > 1 {
			prefix := parts[0]
			return prefix == "log" || prefix == "comment" || prefix == "handoff" || prefix == "file" || prefix == "dep" || prefix == "epic"
		}
		return false
	case *FunctionCall:
		return node.Name == "blocks" || node.Name == "blocked_by" || node.Name == "linked_to" || node.Name == "descendant_of" || node.Name == "rework" || node.Name == "is_ready" || node.Name == "has_open_deps"
	default:
		return false
	}
}

func (e *Evaluator) resolveValue(v interface{}) interface{} {
	switch val := v.(type) {
	case *SpecialValue:
		if val.Type == "me" {
			return e.ctx.CurrentSession
		}
		return val
	case *DateValue:
		return e.resolveDate(val)
	default:
		return v
	}
}

func (e *Evaluator) resolveDate(d *DateValue) interface{} {
	if !d.Relative {
		return d.Raw
	}

	now := e.ctx.Now

	switch d.Raw {
	case "today":
		return now.Format("2006-01-02")
	case "yesterday":
		return now.AddDate(0, 0, -1).Format("2006-01-02")
	case "this_week":
		// Start of current week (Monday)
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return now.AddDate(0, 0, -(weekday - 1)).Format("2006-01-02")
	case "last_week":
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return now.AddDate(0, 0, -(weekday-1)-7).Format("2006-01-02")
	case "this_month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	case "last_month":
		return time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
	default:
		// Parse relative offset like -7d, +3w
		return e.parseRelativeOffset(d.Raw)
	}
}

func (e *Evaluator) parseRelativeOffset(s string) string {
	if len(s) < 2 {
		return s
	}

	sign := 1
	start := 0
	switch s[0] {
	case '-':
		sign = -1
		start = 1
	case '+':
		start = 1
	}

	unit := s[len(s)-1]
	numStr := s[start : len(s)-1]
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return s
	}

	num *= sign
	now := e.ctx.Now

	switch unit {
	case 'd':
		return now.AddDate(0, 0, num).Format("2006-01-02")
	case 'w':
		return now.AddDate(0, 0, num*7).Format("2006-01-02")
	case 'm':
		return now.AddDate(0, num, 0).Format("2006-01-02")
	case 'h':
		return now.Add(time.Duration(num) * time.Hour).Format("2006-01-02 15:04:05")
	default:
		return s
	}
}

// compareDates compares a timestamp against a date literal.
//
// A day-granular literal compares calendar days, so `created <= 2026-05-09`
// includes everything that happened on the 9th and `created > 2026-05-09`
// excludes it. Comparing instants instead would make `<=` identical to `<` and
// `>` identical to `>=`, which is a trap rather than a feature. A literal that
// carries a time compares instants.
func compareDates(ts time.Time, lit dateLiteral, op string) bool {
	var cmp int
	if lit.dayOnly {
		cmp = compareInt(dayOrdinal(ts), dayOrdinal(lit.t))
	} else {
		cmp = ts.Compare(lit.t)
	}

	switch op {
	case OpEq:
		return cmp == 0
	case OpNeq:
		return cmp != 0
	case OpLt:
		return cmp < 0
	case OpGt:
		return cmp > 0
	case OpLte:
		return cmp <= 0
	case OpGte:
		return cmp >= 0
	default:
		return false
	}
}

// dayOrdinal collapses a timestamp to a sortable calendar day in its own
// offset, so two timestamps written under different UTC offsets still compare
// by the wall-clock day the user saw.
func dayOrdinal(t time.Time) int {
	y, m, d := t.Date()
	return y*10000 + int(m)*100 + d
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// nodeToMatcher converts a node to an in-memory matcher function
func (e *Evaluator) nodeToMatcher(n Node) (func(models.Issue) bool, error) {
	switch node := n.(type) {
	case *BinaryExpr:
		leftMatcher, err := e.nodeToMatcher(node.Left)
		if err != nil {
			return nil, err
		}
		rightMatcher, err := e.nodeToMatcher(node.Right)
		if err != nil {
			return nil, err
		}
		if node.Op == OpAnd {
			return func(i models.Issue) bool {
				return leftMatcher(i) && rightMatcher(i)
			}, nil
		}
		return func(i models.Issue) bool {
			return leftMatcher(i) || rightMatcher(i)
		}, nil

	case *UnaryExpr:
		// If the inner expression is a cross-entity condition, don't apply NOT here
		// The negation will be handled by the cross-entity filter in execute.go
		if e.isCrossEntityNode(node.Expr) {
			return func(i models.Issue) bool { return true }, nil
		}
		matcher, err := e.nodeToMatcher(node.Expr)
		if err != nil {
			return nil, err
		}
		return func(i models.Issue) bool {
			return !matcher(i)
		}, nil

	case *FieldExpr:
		return e.fieldExprToMatcher(node)

	case *FunctionCall:
		return e.functionToMatcher(node)

	case *TextSearch:
		pattern := strings.ToLower(node.Text)
		return func(i models.Issue) bool {
			return strings.Contains(strings.ToLower(i.ID), pattern) ||
				strings.Contains(strings.ToLower(i.Title), pattern) ||
				strings.Contains(strings.ToLower(i.Description), pattern)
		}, nil

	default:
		return nil, fmt.Errorf("unsupported node type for matcher: %T", n)
	}
}

func (e *Evaluator) fieldExprToMatcher(node *FieldExpr) (func(models.Issue) bool, error) {
	field := node.Field
	value := e.resolveValue(node.Value)

	// Get field value getter
	getter := e.getFieldGetter(field)
	if getter == nil {
		return func(models.Issue) bool { return true }, nil
	}

	switch node.Operator {
	case OpEq:
		return func(i models.Issue) bool {
			return e.compareEqual(getter(i), value)
		}, nil
	case OpNeq:
		return func(i models.Issue) bool {
			return !e.compareEqual(getter(i), value)
		}, nil
	case OpContains:
		pattern := strings.ToLower(fmt.Sprintf("%v", value))
		return func(i models.Issue) bool {
			fieldVal := strings.ToLower(fmt.Sprintf("%v", getter(i)))
			return strings.Contains(fieldVal, pattern)
		}, nil
	case OpNotContains:
		pattern := strings.ToLower(fmt.Sprintf("%v", value))
		return func(i models.Issue) bool {
			fieldVal := strings.ToLower(fmt.Sprintf("%v", getter(i)))
			return !strings.Contains(fieldVal, pattern)
		}, nil
	case OpLt, OpGt, OpLte, OpGte:
		return func(i models.Issue) bool {
			return e.compareOrder(getter(i), value, node.Operator)
		}, nil
	default:
		return func(models.Issue) bool { return true }, nil
	}
}

func (e *Evaluator) getFieldGetter(field string) func(models.Issue) interface{} {
	switch field {
	case "id":
		return func(i models.Issue) interface{} { return i.ID }
	case "title":
		return func(i models.Issue) interface{} { return i.Title }
	case "description":
		return func(i models.Issue) interface{} { return i.Description }
	case "status":
		return func(i models.Issue) interface{} { return string(i.Status) }
	case "type":
		return func(i models.Issue) interface{} { return string(i.Type) }
	case "priority":
		return func(i models.Issue) interface{} { return string(i.Priority) }
	case "points":
		return func(i models.Issue) interface{} { return i.Points }
	case "labels":
		return func(i models.Issue) interface{} { return strings.Join(i.Labels, ",") }
	case "parent", "parent_id":
		return func(i models.Issue) interface{} { return i.ParentID }
	case "implementer", "implementer_session":
		return func(i models.Issue) interface{} { return i.ImplementerSession }
	case "reviewer", "reviewer_session":
		return func(i models.Issue) interface{} { return i.ReviewerSession }
	case "branch", "created_branch":
		return func(i models.Issue) interface{} { return i.CreatedBranch }
	case "sprint":
		return func(i models.Issue) interface{} { return i.Sprint }
	case "minor":
		return func(i models.Issue) interface{} { return i.Minor }
	case "defer_count":
		return func(i models.Issue) interface{} { return i.DeferCount }
	case "due", "due_date", "defer", "defer_until":
		return func(i models.Issue) interface{} {
			date := i.DueDate
			if field == "defer" || field == "defer_until" {
				date = i.DeferUntil
			}
			if date == nil {
				return nil
			}
			value, err := time.ParseInLocation("2006-01-02", *date, e.location())
			if err != nil {
				return nil
			}
			return value
		}
	case "created", "created_at":
		return func(i models.Issue) interface{} { return i.CreatedAt }
	case "updated", "updated_at":
		return func(i models.Issue) interface{} { return i.UpdatedAt }
	case "closed", "closed_at":
		return func(i models.Issue) interface{} {
			if i.ClosedAt != nil {
				return *i.ClosedAt
			}
			return nil
		}
	default:
		return nil
	}
}

func (e *Evaluator) compareEqual(a, b interface{}) bool {
	if sv, ok := b.(*SpecialValue); ok {
		switch sv.Type {
		case "empty":
			return a == nil || a == "" || a == 0
		case "null":
			return a == nil
		}
	}
	// Timestamps never string-compare usefully against a date literal
	// ("2026-05-09 12:00:00 -0700 PDT" != "2026-05-09"), so compare them as
	// dates. A NULL timestamp matches nothing.
	if ts, isTime := a.(time.Time); isTime {
		lit, ok := parseDateLiteral(fmt.Sprintf("%v", b), e.location())
		if !ok {
			return false
		}
		return compareDates(ts, lit, OpEq)
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func (e *Evaluator) compareOrder(a, b interface{}, op string) bool {
	// Date comparison: a timestamp field against a resolved date literal.
	// Without this the values fall through to toNumber(), which turns the
	// timestamp into a Unix second count and the literal into 0 — making every
	// `<` false and every `>` true regardless of the dates involved.
	if ts, isTime := a.(time.Time); isTime {
		lit, ok := parseDateLiteral(fmt.Sprintf("%v", b), e.location())
		if !ok {
			return false
		}
		return compareDates(ts, lit, op)
	}
	// A NULL timestamp (e.g. closed on an open issue) matches no ordering
	// comparison, the same way SQL treats NULL.
	if a == nil {
		return false
	}

	// Handle priority comparison specially
	if priorityA, okA := a.(string); okA {
		if priorityB, okB := b.(string); okB {
			if isPriority(priorityA) && isPriority(priorityB) {
				return comparePriority(priorityA, priorityB, op)
			}
		}
	}

	// Try numeric comparison
	numA := toNumber(a)
	numB := toNumber(b)

	switch op {
	case OpLt:
		return numA < numB
	case OpGt:
		return numA > numB
	case OpLte:
		return numA <= numB
	case OpGte:
		return numA >= numB
	default:
		return false
	}
}

// location returns the timezone date literals are interpreted in. Timestamps
// are stored with the offset that was local when they were written, so the
// caller's local zone is the wall clock a query means.
func (e *Evaluator) location() *time.Location {
	if e.ctx != nil && !e.ctx.Now.IsZero() {
		return e.ctx.Now.Location()
	}
	return time.Local
}

func (e *Evaluator) functionToMatcher(node *FunctionCall) (func(models.Issue) bool, error) {
	switch node.Name {
	case "has":
		if len(node.Args) < 1 {
			return nil, fmt.Errorf("has() requires 1 argument")
		}
		field := fmt.Sprintf("%v", node.Args[0])
		getter := e.getFieldGetter(field)
		if getter == nil {
			return func(models.Issue) bool { return false }, nil
		}
		return func(i models.Issue) bool {
			v := getter(i)
			if v == nil {
				return false
			}
			if s, ok := v.(string); ok {
				return s != ""
			}
			return true
		}, nil

	case "is":
		if len(node.Args) < 1 {
			return nil, fmt.Errorf("is() requires 1 argument")
		}
		status := fmt.Sprintf("%v", node.Args[0])
		return func(i models.Issue) bool {
			return strings.EqualFold(string(i.Status), status)
		}, nil

	case "any":
		if len(node.Args) < 2 {
			return nil, fmt.Errorf("any() requires at least 2 arguments")
		}
		field := fmt.Sprintf("%v", node.Args[0])
		getter := e.getFieldGetter(field)
		if getter == nil {
			return func(models.Issue) bool { return false }, nil
		}
		values := make([]string, len(node.Args)-1)
		for i := 1; i < len(node.Args); i++ {
			values[i-1] = strings.ToLower(fmt.Sprintf("%v", e.resolveValue(node.Args[i])))
		}
		return func(i models.Issue) bool {
			fieldVal := strings.ToLower(fmt.Sprintf("%v", getter(i)))
			for _, v := range values {
				if fieldVal == v {
					return true
				}
			}
			return false
		}, nil

	case "all":
		if len(node.Args) < 2 {
			return nil, fmt.Errorf("all() requires at least 2 arguments")
		}
		field := fmt.Sprintf("%v", node.Args[0])
		getter := e.getFieldGetter(field)
		if getter == nil {
			return func(models.Issue) bool { return false }, nil
		}
		values := make([]string, len(node.Args)-1)
		for i := 1; i < len(node.Args); i++ {
			values[i-1] = strings.ToLower(fmt.Sprintf("%v", e.resolveValue(node.Args[i])))
		}
		return func(i models.Issue) bool {
			fieldVal := strings.ToLower(fmt.Sprintf("%v", getter(i)))
			for _, v := range values {
				if !strings.Contains(fieldVal, v) {
					return false
				}
			}
			return true
		}, nil

	case "none":
		if len(node.Args) < 2 {
			return nil, fmt.Errorf("none() requires at least 2 arguments")
		}
		field := fmt.Sprintf("%v", node.Args[0])
		getter := e.getFieldGetter(field)
		if getter == nil {
			return func(models.Issue) bool { return true }, nil
		}
		values := make([]string, len(node.Args)-1)
		for i := 1; i < len(node.Args); i++ {
			values[i-1] = strings.ToLower(fmt.Sprintf("%v", e.resolveValue(node.Args[i])))
		}
		return func(i models.Issue) bool {
			fieldVal := strings.ToLower(fmt.Sprintf("%v", getter(i)))
			for _, v := range values {
				if fieldVal == v || strings.Contains(fieldVal, v) {
					return false
				}
			}
			return true
		}, nil

	case "child_of":
		if len(node.Args) < 1 {
			return nil, fmt.Errorf("child_of() requires 1 argument")
		}
		parentID := fmt.Sprintf("%v", node.Args[0])
		return func(i models.Issue) bool {
			return i.ParentID == parentID
		}, nil

	case "descendant_of":
		// This requires recursive parent traversal, handled via cross-entity filter
		// Return placeholder that allows issue through (will be filtered in Execute)
		return func(models.Issue) bool { return true }, nil

	case "blocks", "blocked_by", "linked_to", "rework", "is_ready", "has_open_deps":
		// These use related facts, handled via cross-entity filter
		return func(models.Issue) bool { return true }, nil

	case "label", "labels":
		if len(node.Args) < 1 {
			return nil, fmt.Errorf("%s() requires 1 argument", node.Name)
		}
		label := strings.ToLower(fmt.Sprintf("%v", node.Args[0]))
		return func(i models.Issue) bool {
			for _, l := range i.Labels {
				if strings.ToLower(l) == label {
					return true
				}
			}
			return false
		}, nil

	default:
		return nil, fmt.Errorf("unknown function: %s", node.Name)
	}
}

// Helper functions

var priorityRegexp = regexp.MustCompile(`(?i)^P[0-4]$`)

func isPriority(s string) bool {
	return priorityRegexp.MatchString(s)
}

func comparePriority(a, b, op string) bool {
	// Normalize to uppercase for consistent map lookup
	a = strings.ToUpper(a)
	b = strings.ToUpper(b)
	// P0 is highest priority (lowest number)
	priorityOrder := map[string]int{"P0": 0, "P1": 1, "P2": 2, "P3": 3, "P4": 4}
	orderA, okA := priorityOrder[a]
	orderB, okB := priorityOrder[b]
	if !okA || !okB {
		return false
	}

	switch op {
	case OpLt:
		return orderA < orderB
	case OpGt:
		return orderA > orderB
	case OpLte:
		return orderA <= orderB
	case OpGte:
		return orderA >= orderB
	default:
		return false
	}
}

func toNumber(v interface{}) float64 {
	switch val := v.(type) {
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case float64:
		return val
	case string:
		if n, err := strconv.ParseFloat(val, 64); err == nil {
			return n
		}
	case time.Time:
		return float64(val.Unix())
	}
	return 0
}
