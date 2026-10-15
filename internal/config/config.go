package config

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"time"
)

const (
	EngineSQLite   = "sqlite"
	EnginePostgres = "postgres"
)

type Config struct {
	Address        string
	DBPath         string
	BootstrapToken string
	Readers        int
	BusyTimeout    time.Duration
	Engine         string
	DatabaseURL    string
}

func Load(args []string, getenv func(string) string) (Config, error) {
	c := Config{Address: "127.0.0.1:7000", DBPath: "./data/td-backend.db", Readers: 4, BusyTimeout: 5 * time.Second, Engine: EngineSQLite}
	if v := getenv("TD_BACKEND_ADDR"); v != "" {
		c.Address = v
	}
	if v := getenv("TD_BACKEND_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := getenv("TD_BACKEND_DB_ENGINE"); v != "" {
		c.Engine = v
	}
	c.DatabaseURL = getenv("TD_BACKEND_DATABASE_URL")
	c.BootstrapToken = getenv("TD_BACKEND_BOOTSTRAP_TOKEN")
	fs := flag.NewFlagSet("td-backend", flag.ContinueOnError)
	fs.StringVar(&c.Address, "addr", c.Address, "HTTP listen address")
	fs.StringVar(&c.DBPath, "db", c.DBPath, "SQLite database path")
	fs.StringVar(&c.Engine, "db-engine", c.Engine, "database engine: sqlite or postgres")
	fs.IntVar(&c.Readers, "readers", c.Readers, "maximum reader connections")
	fs.DurationVar(&c.BusyTimeout, "busy-timeout", c.BusyTimeout, "SQLite lock wait")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, fmt.Errorf("unexpected positional arguments")
	}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return c, fmt.Errorf("invalid listen address: %w", err)
	}
	switch c.Engine {
	case EngineSQLite:
		if c.DatabaseURL != "" || c.DBPath == "" || c.DBPath == ":memory:" || c.Readers < 1 || c.BusyTimeout < time.Millisecond {
			return c, fmt.Errorf("invalid SQLite database configuration")
		}
	case EnginePostgres:
		if c.DatabaseURL == "" || fsFlagSet(fs, "db") || getenv("TD_BACKEND_DB_PATH") != "" || fsFlagSet(fs, "readers") || fsFlagSet(fs, "busy-timeout") {
			return c, fmt.Errorf("postgres requires TD_BACKEND_DATABASE_URL and does not accept SQLite options")
		}
		u, err := url.Parse(c.DatabaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path == "" || u.Path == "/" {
			return c, fmt.Errorf("invalid TD_BACKEND_DATABASE_URL")
		}
	default:
		return c, fmt.Errorf("invalid database engine %q (use sqlite or postgres)", c.Engine)
	}
	return c, nil
}

func fsFlagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
