package httpapi

import (
	"io"
	"net/http"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/board"
	"github.com/yoophi/td-backend/internal/application/port"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerBoards(mux *http.ServeMux, svc *board.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	const base = "/v1/projects/{project_id}/boards"
	mux.HandleFunc("GET "+base, protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		boards, e := svc.List(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		data := []receiptcodec.BoardDTO{}
		for _, b := range boards {
			data = append(data, receiptcodec.BoardDTOFrom(b))
		}
		writeSuccess(w, 200, struct {
			Boards []receiptcodec.BoardDTO `json:"boards"`
		}{data})
	}))
	finish := func(w http.ResponseWriter, reply port.StoredReply, e error) {
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}
	mux.HandleFunc("POST "+base, protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd board.CreateCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Create(r.Context(), caller(r), m, cmd)
		finish(w, reply, e)
	}))
	mux.HandleFunc("PATCH "+base+"/{board_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd board.PatchCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Patch(r.Context(), caller(r), m, r.PathValue("board_id"), r.Header.Get("If-Match"), cmd)
		finish(w, reply, e)
	}))
	mux.HandleFunc("POST "+base+"/{board_id}/issues", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd board.PositionCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Position(r.Context(), caller(r), m, r.PathValue("board_id"), r.Header.Get("If-Match"), cmd)
		finish(w, reply, e)
	}))
	for _, route := range []struct{ pattern, action string }{{"DELETE " + base + "/{board_id}", "delete"}, {"POST " + base + "/{board_id}/visit", "visit"}, {"DELETE " + base + "/{board_id}/issues/{issue_id}", "unpin"}} {
		mux.HandleFunc(route.pattern, protect(func(w http.ResponseWriter, r *http.Request) {
			body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
			if e != nil || len(body) != 0 {
				writeError(w, invalid("body", "request body must be empty"))
				return
			}
			m, e := mutation(r, struct{}{})
			if e != nil {
				writeError(w, e)
				return
			}
			c, id, revision := caller(r), r.PathValue("board_id"), r.Header.Get("If-Match")
			var reply port.StoredReply
			switch route.action {
			case "delete":
				reply, e = svc.Delete(r.Context(), c, m, id, revision)
			case "visit":
				reply, e = svc.Visit(r.Context(), c, m, id, revision)
			case "unpin":
				reply, e = svc.Unpin(r.Context(), c, m, id, revision, r.PathValue("issue_id"))
			}
			finish(w, reply, e)
		}))
	}
}
