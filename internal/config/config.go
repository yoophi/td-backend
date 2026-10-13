package config

import (
	"flag"
	"fmt"
	"net"
	"time"
)

type Config struct {
	Address        string
	DBPath         string
	BootstrapToken string
	Readers        int
	BusyTimeout    time.Duration
}

func Load(args []string, getenv func(string) string) (Config, error) {
	c := Config{Address: "127.0.0.1:7000", DBPath: "./data/td-backend.db", Readers: 4, BusyTimeout: 5 * time.Second}
	if v := getenv("TD_BACKEND_ADDR"); v != "" {
		c.Address = v
	}
	if v := getenv("TD_BACKEND_DB_PATH"); v != "" {
		c.DBPath = v
	}
	c.BootstrapToken = getenv("TD_BACKEND_BOOTSTRAP_TOKEN")
	fs := flag.NewFlagSet("td-backend", flag.ContinueOnError)
	fs.StringVar(&c.Address, "addr", c.Address, "HTTP listen address")
	fs.StringVar(&c.DBPath, "db", c.DBPath, "SQLite database path")
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
	if c.DBPath == "" || c.DBPath == ":memory:" || c.Readers < 1 || c.BusyTimeout < time.Millisecond {
		return c, fmt.Errorf("invalid database configuration")
	}
	return c, nil
}
