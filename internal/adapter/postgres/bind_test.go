package postgres

import "testing"

func TestBindPreservesQuotedQuestionMarks(t *testing.T) {
	got := bind(`SELECT '?' AS literal, "?" AS identifier FROM issues WHERE id=? AND title='it''s ?' AND project_id=?`)
	want := `SELECT '?' AS literal, "?" AS identifier FROM issues WHERE id=$1 AND title='it''s ?' AND project_id=$2`
	if got != want {
		t.Fatalf("bind: %q", got)
	}
}

func TestNormalizeArgsDoesNotMutateCaller(t *testing.T) {
	args := []any{"id", true, false}
	converted := normalizeArgs(args)
	if converted[1] != 1 || converted[2] != 0 || args[1] != true || args[2] != false {
		t.Fatalf("converted=%v original=%v", converted, args)
	}
}
