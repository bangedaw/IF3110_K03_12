package sqlsplit

import "testing"

func TestSplitQuotedSemicolonsAndDollarQuote(t *testing.T) {
	script := `CREATE FUNCTION f() RETURNS void AS $body$ BEGIN PERFORM ';'; END; $body$ LANGUAGE plpgsql;
SELECT 'a;b', "semi;colon"; -- comment ;
SELECT 3;`
	got, err := Split(script, PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d statements: %#v", len(got), got)
	}
}
func TestSplitNestedBlockComments(t *testing.T) {
	got, err := Split("SELECT 1 /* outer /* inner ; */ still outer */; SELECT 2;", PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
}
func TestMySQLBacktickAndEscapedQuote(t *testing.T) {
	got, err := Split("SELECT `a;b`, 'it\\'s;ok'; SELECT 2;", MySQL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
}
func TestValidateForbidden(t *testing.T) {
	for _, sql := range []string{"BEGIN;", "COMMIT;", "ROLLBACK;", "SAVEPOINT x;", "VACUUM;", "CREATE INDEX CONCURRENTLY x ON t(id);", "CREATE DATABASE x;"} {
		s, err := Split(sql, PostgreSQL)
		if err != nil {
			t.Fatal(err)
		}
		if Validate(s, PostgreSQL) == nil {
			t.Errorf("expected rejection: %s", sql)
		}
	}
}
func TestValidateAllowsFunctionBody(t *testing.T) {
	s, err := Split(`CREATE FUNCTION f() RETURNS void AS $$ BEGIN PERFORM 1; END; $$ LANGUAGE plpgsql;`, PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(s, PostgreSQL); err != nil {
		t.Fatal(err)
	}
}
