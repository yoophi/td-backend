package config

import "testing"

func TestConfigurationRejectsUnsafeOrInvalidStartup(t *testing.T) {
	for _, args := range [][]string{{"--db", ":memory:"}, {"--readers", "0"}, {"--addr", "missing-port"}, {"--busy-timeout", "0s"}, {"unexpected"}} {
		if _, err := Load(args, func(string) string { return "" }); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	c, err := Load([]string{"--addr", "127.0.0.1:8000"}, func(name string) string {
		switch name {
		case "TD_BACKEND_DB_PATH":
			return "data/test.db"
		case "TD_BACKEND_ADDR":
			return "127.0.0.1:9000"
		case "TD_BACKEND_BOOTSTRAP_TOKEN":
			return "test-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "127.0.0.1:8000" || c.DBPath != "data/test.db" || c.BootstrapToken != "test-token" {
		t.Fatal("flag/environment precedence")
	}
}

func TestDatabaseEngineSelection(t *testing.T) {
	dsn := "postgres://td@localhost:5432/td_backend?sslmode=disable"
	getenv := func(name string) string {
		switch name {
		case "TD_BACKEND_DB_ENGINE":
			return "postgres"
		case "TD_BACKEND_DATABASE_URL":
			return dsn
		}
		return ""
	}
	c, err := Load(nil, getenv)
	if err != nil || c.Engine != EnginePostgres || c.DatabaseURL != dsn {
		t.Fatalf("postgres config: %#v, %v", c, err)
	}
	c, err = Load([]string{"--db-engine=sqlite"}, func(string) string { return "" })
	if err != nil || c.Engine != EngineSQLite || c.DBPath != "./data/td-backend.db" {
		t.Fatalf("sqlite default: %#v, %v", c, err)
	}
	for _, args := range [][]string{{"--db-engine=postgres", "--db=other.db"}, {"--db-engine=postgres", "--readers=2"}, {"--db-engine=postgres", "--busy-timeout=1s"}} {
		if _, err := Load(args, getenv); err == nil {
			t.Fatalf("accepted incompatible flags: %v", args)
		}
	}
	for _, input := range []string{"", "sqlite:///td", "postgres://localhost", "postgres://localhost/"} {
		if _, err := Load([]string{"--db-engine=postgres"}, func(name string) string {
			if name == "TD_BACKEND_DATABASE_URL" {
				return input
			}
			return ""
		}); err == nil {
			t.Fatalf("accepted invalid DSN: %q", input)
		}
	}
}
