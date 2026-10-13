package receiptcodec

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/yoophi/td-backend/internal/application/port"
)

type OperationDTO struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Status         string          `json:"status"`
	ResponseStatus int             `json:"response_status"`
	Response       json.RawMessage `json:"response"`
	CreatedAt      time.Time       `json:"created_at"`
}

func OperationDTOFrom(r port.Receipt) (OperationDTO, error) {
	var body map[string]json.RawMessage
	if e := json.Unmarshal(r.Reply.Body, &body); e != nil || body == nil {
		return OperationDTO{}, fmt.Errorf("invalid stored operation response")
	}
	return OperationDTO{r.Key, r.SessionID, r.Method, r.Path, "committed", r.Reply.Status, json.RawMessage(r.Reply.Body), r.CreatedAt}, nil
}
