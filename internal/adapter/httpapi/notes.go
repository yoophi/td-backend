package httpapi

import (
	"io"
	"net/http"

	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/application/note"
	"github.com/yoophi/td-backend/internal/domain"
)

func registerNotes(mux *http.ServeMux, svc *note.Service, protect func(http.HandlerFunc) http.HandlerFunc, caller func(*http.Request) domain.Caller) {
	const base = "/v1/projects/{project_id}/notes"
	mux.HandleFunc("GET "+base, protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		notes, e := svc.List(r.Context(), caller(r))
		if e != nil {
			writeError(w, e)
			return
		}
		data := []receiptcodec.NoteDTO{}
		for _, n := range notes {
			data = append(data, receiptcodec.NoteDTOFrom(n))
		}
		writeSuccess(w, 200, struct {
			Notes []receiptcodec.NoteDTO `json:"notes"`
		}{data})
	}))
	mux.HandleFunc("GET "+base+"/{note_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) > 0 {
			writeError(w, invalid("query", "unexpected query parameter"))
			return
		}
		n, e := svc.Get(r.Context(), caller(r), r.PathValue("note_id"))
		if e != nil {
			writeError(w, e)
			return
		}
		w.Header().Set("ETag", `"`+domain.Revision(n.Revision)+`"`)
		writeSuccess(w, 200, struct {
			Note receiptcodec.NoteDTO `json:"note"`
		}{receiptcodec.NoteDTOFrom(n)})
	}))
	mux.HandleFunc("POST "+base, protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd note.CreateCommand
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
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("PATCH "+base+"/{note_id}", protect(func(w http.ResponseWriter, r *http.Request) {
		var cmd note.PatchCommand
		if e := decode(w, r, &cmd); e != nil {
			writeError(w, e)
			return
		}
		m, e := mutation(r, cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		reply, e := svc.Patch(r.Context(), caller(r), m, r.PathValue("note_id"), r.Header.Get("If-Match"), cmd)
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
	mux.HandleFunc("DELETE "+base+"/{note_id}", protect(func(w http.ResponseWriter, r *http.Request) {
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
		reply, e := svc.Delete(r.Context(), caller(r), m, r.PathValue("note_id"), r.Header.Get("If-Match"))
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}
