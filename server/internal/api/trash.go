package api

// The trash (store/trash.go): deleting a key, a service or an app moves it
// here for seven days. GET /api/admin/trash lists it, POST
// /api/admin/trash/{id}/restore brings an item back as it was, and a
// superadmin's DELETE /api/admin/trash/{id} deletes one for good; the
// janitor (Store.Prune) deletes the rest once their time is up. Hidden key
// rules hold here too: an item that is or holds a hidden key is listed,
// restored and purged only by a superadmin, and is "not found" to anyone
// else. No trash answer ever carries a secret.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sidharthpunathil/acnologia/server/internal/store"
)

// trashAnswer is a delete's answer when the item went to the trash.
func trashAnswer(it *store.TrashItem) map[string]any {
	return map[string]any{"ok": true, "restore_id": it.ID, "expires_at": it.ExpiresAt, "trash": it}
}

// trashDetail is the audit detail of a move to the trash.
func trashDetail(it *store.TrashItem, extra string) string {
	d := "moved to the trash, restore id " + it.ID
	if extra != "" {
		d = extra + "; " + d
	}
	return d
}

func (s *Server) listTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListTrash(r.Context(), isSuper(r))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "ttl_days": int(store.TrashTTL.Hours() / 24)})
}

// loadTrash reads the {id} entry, hiding hidden items from non superadmins.
func (s *Server) loadTrash(w http.ResponseWriter, r *http.Request) (*store.TrashItem, bool) {
	it, err := s.Store.GetTrash(r.Context(), chi.URLParam(r, "id"))
	if err == nil && it.Hidden && !isSuper(r) {
		err = store.ErrNotFound
	}
	if err != nil {
		writeStoreErr(w, err)
		return nil, false
	}
	return it, true
}

func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.loadTrash(w, r); !ok {
		return
	}
	it, err := s.Store.RestoreTrash(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, store.ErrRestoreConflict):
		writeErr(w, 409, "restore_conflict", restoreMsg(err))
		return
	case errors.Is(err, store.ErrRestoreBlocked):
		writeErr(w, 409, "restore_blocked", restoreMsg(err))
		return
	case err != nil:
		writeStoreErr(w, err)
		return
	}
	switch it.Kind {
	case store.TrashService:
		s.forgetRotating()
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "trash.restore", it.Kind+" "+trashTarget(it), "restore id "+it.ID)
	writeJSON(w, 200, map[string]any{"ok": true, "restored": it})
}

// restoreMsg drops the error's sentinel prefix.
func restoreMsg(err error) string {
	msg := err.Error()
	if _, rest, ok := strings.Cut(msg, ": "); ok {
		return rest
	}
	return msg
}

func (s *Server) purgeTrash(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.loadTrash(w, r); !ok {
		return
	}
	it, err := s.Store.PurgeTrash(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	switch it.Kind {
	case store.TrashService:
		s.Cache.PurgePrefix(servicePrefix(it.TargetID))
		s.mcpSessions.deleteService(it.TargetID)
		s.forgetRotating()
	case store.TrashApp:
		s.Cache.PurgePrefix(idempotencyPrefix(it.TargetID))
	}
	s.Store.AddAudit(r.Context(), userFrom(r), "trash.purge", it.Kind+" "+trashTarget(it), "deleted for good before its time was up")
	writeJSON(w, 200, map[string]any{"ok": true, "purged": it})
}

// trashTarget names an item in the audit log: a key with its service.
func trashTarget(it *store.TrashItem) string {
	if it.Kind == store.TrashKey && it.Service != "" {
		return fmt.Sprintf("%s/%s", it.Service, it.Name)
	}
	return it.Name
}
