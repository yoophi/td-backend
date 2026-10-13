package receiptcodec

import (
	"encoding/json"
	"fmt"
	"github.com/yoophi/td-backend/internal/application/port"
)

type Codec struct{}

func (Codec) Encode(kind string, outcome any) (port.StoredReply, error) {
	status := 201
	switch kind {
	case "project.create", "session.create":
	default:
		return port.StoredReply{}, fmt.Errorf("unknown reply kind %q", kind)
	}
	body, e := json.Marshal(struct {
		OK   bool `json:"ok"`
		Data any  `json:"data"`
	}{true, outcome})
	if e != nil {
		return port.StoredReply{}, e
	}
	return port.StoredReply{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: append(body, '\n')}, nil
}
