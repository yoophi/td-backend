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
		includeDeleted, e := noteIncludeDeleted(r)
		if e != nil {
			writeError(w, e)
			return
		}
		var notes []domain.Note
		if includeDeleted {
			notes, e = svc.ListIncludingDeleted(r.Context(), caller(r))
		} else {
			notes, e = svc.List(r.Context(), caller(r))
		}
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
		includeDeleted, e := noteIncludeDeleted(r)
		if e != nil {
			writeError(w, e)
			return
		}
		var n domain.Note
		if includeDeleted {
			n, e = svc.GetIncludingDeleted(r.Context(), caller(r), r.PathValue("note_id"))
		} else {
			n, e = svc.Get(r.Context(), caller(r), r.PathValue("note_id"))
		}
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
	mux.HandleFunc("POST "+base+"/{note_id}/restore", protect(func(w http.ResponseWriter, r *http.Request) {
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
		reply, e := svc.Restore(r.Context(), caller(r), m, r.PathValue("note_id"), r.Header.Get("If-Match"))
		if e != nil {
			writeError(w, e)
			return
		}
		writeReply(w, reply)
	}))
}

func noteIncludeDeleted(r *http.Request) (bool, error) {
	query := r.URL.Query()
	if len(query) == 0 {
		return false, nil
	}
	values, ok := query["include_deleted"]
	if len(query) != 1 || !ok || len(values) != 1 || (values[0] != "true" && values[0] != "false") {
		return false, invalid("include_deleted", "expected include_deleted=true or false")
	}
	return values[0] == "true", nil
}
