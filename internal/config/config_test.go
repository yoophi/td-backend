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
