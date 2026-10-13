package identity

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"github.com/yoophi/td-backend/internal/domain"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRFCUUIDBitsTimestampAndEntropy(t *testing.T) {
	now := time.Date(2026, 10, 13, 7, 0, 0, 123000000, time.UTC)
	g := Generator{Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{0xff}, 32))}
	id, e := g.ID("project")
	if e != nil || !domain.ValidProjectID(id) {
		t.Fatal(id, e)
	}
	raw, e := hex.DecodeString(strings.ReplaceAll(strings.TrimPrefix(id, "prj_"), "-", ""))
	if e != nil {
		t.Fatal(e)
	}
	timestamp := binary.BigEndian.Uint64(append([]byte{0, 0}, raw[:6]...))
	if timestamp != uint64(now.UnixMilli()) || raw[6] != 0x7f || raw[8] != 0xbf {
		t.Fatal("UUIDv7 layout", raw)
	}
	key, e := g.UUID4()
	if e != nil || !domain.ValidOperationKey(key) {
		t.Fatal(key, e)
	}
	g.Random = bytes.NewReader(nil)
	if _, e = g.ID("project"); !errors.Is(e, io.EOF) {
		t.Fatal("entropy failure", e)
	}
	for _, invalid := range []string{id[4:], strings.ToUpper(id), id[:len(id)-1], strings.Replace(id, "-7fff-", "-4fff-", 1)} {
		if domain.ValidProjectID(invalid) {
			t.Fatal("accepted invalid ID", invalid)
		}
	}
}
func TestRepeatedMillisecondHasIndependentIDs(t *testing.T) {
	g := New()
	g.Now = func() time.Time { return time.UnixMilli(1000) }
	ids := map[string]bool{}
	for range 1000 {
		id, e := g.ID("project")
		if e != nil {
			t.Fatal(e)
		}
		if ids[id] {
			t.Fatal("duplicate ID")
		}
		ids[id] = true
	}
}
