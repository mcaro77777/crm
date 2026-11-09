package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
)

func (s *Servidor) estadoDespachoConversacion(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	conversacionID := r.PathValue("id")
	var existe bool
	err := s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM conversation WHERE organization_id=? AND id=?)`, sesion.OrganizacionID, conversacionID).Scan(&existe)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.errorInterno(w, "comprobar conversacion para despacho", err)
		return
	}
	if !existe {
		responderError(w, http.StatusNotFound, "not_found", "Conversacion no encontrada", nil)
		return
	}
	estado, err := s.despachos.Estado(r.Context(), sesion.OrganizacionID, conversacionID)
	if err != nil {
		s.errorInterno(w, "leer estado del despacho", err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"dispatch": estado})
}
