// Package identity supplies CSPRNG IDs; policy validation lives in domain.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

type Generator struct {
	Now    func() time.Time
	Random io.Reader
}

func New() Generator { return Generator{Now: time.Now, Random: rand.Reader} }
func (g Generator) UUID7() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(g.Random, b[:]); err != nil {
		return "", fmt.Errorf("ID entropy: %w", err)
	}
	ms := g.Now().UnixMilli()
	if ms < 0 || uint64(ms) >= 1<<48 {
		return "", fmt.Errorf("UUIDv7 timestamp out of range")
	}
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	b[6] = (b[6] & 15) | 0x70
	b[8] = (b[8] & 63) | 0x80
	return formatUUID(b), nil
}
func (g Generator) UUID4() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(g.Random, b[:]); err != nil {
		return "", fmt.Errorf("ID entropy: %w", err)
	}
	b[6] = (b[6] & 15) | 0x40
	b[8] = (b[8] & 63) | 0x80
	return formatUUID(b), nil
}
func formatUUID(b [16]byte) string {
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func (g Generator) ID(kind string) (string, error) {
	switch kind {
	case "project":
		s, e := g.UUID7()
		if e != nil {
			return "", e
		}
		return "prj_" + s, nil
	case "operation":
		return g.UUID7()
	case "session":
		s, e := g.UUID7()
		if e != nil {
			return "", e
		}
		return "ses_" + s, nil
	default:
		prefix := map[string]string{"board": "bd-", "issue": "td-", "note": "nt-", "review": "rv-", "activity": "act-", "work_session": "ws-", "file": "ifl_", "dependency": "dep_"}[kind]
		if prefix == "" {
			return "", fmt.Errorf("unknown entity kind %q", kind)
		}
		var b [8]byte
		if _, e := io.ReadFull(g.Random, b[:]); e != nil {
			return "", fmt.Errorf("ID entropy: %w", e)
		}
		return prefix + hex.EncodeToString(b[:]), nil
	}
}
