package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

type respuestaRapidaDTO struct {
	ID            string
	Atajo         string
	Texto         string
	CreadoEn      time.Time
	ActualizadoEn time.Time
}

func (s *Servidor) listarRespuestasRapidas(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,atajo,texto,created_at,updated_at FROM quick_reply WHERE organization_id=? ORDER BY atajo", sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar respuestas rápidas", err)
		return
	}
	defer rows.Close()
	items := make([]respuestaRapidaDTO, 0)
	for rows.Next() {
		var x respuestaRapidaDTO
		if err := rows.Scan(&x.ID, &x.Atajo, &x.Texto, &x.CreadoEn, &x.ActualizadoEn); err != nil {
			s.errorInterno(w, "leer respuesta rápida", err)
			return
		}
		items = append(items, x)
	}
	responderJSON(w, 200, map[string]any{"items": items})
}

func validarRespuestaRapida(atajo, texto string) (string, string, bool) {
	atajo = strings.ToLower(strings.Trim(strings.TrimSpace(atajo), "/"))
	texto = strings.TrimSpace(texto)
	return atajo, texto, atajo != "" && len(atajo) <= 32 && texto != "" && len([]rune(texto)) <= 4096 && !strings.ContainsAny(atajo, " \t\r\n")
}

func (s *Servidor) crearRespuestaRapida(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e struct {
		Atajo string `json:"atajo"`
		Texto string `json:"texto"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	atajo, texto, ok := validarRespuestaRapida(e.Atajo, e.Texto)
	e.Atajo, e.Texto = atajo, texto
	if !ok {
		responderError(w, 422, "validation_error", "Atajo y texto no son válidos", nil)
		return
	}
	id, _ := identificadores.Nuevo("qr")
	ahora := time.Now().UTC()
	_, err := s.db.ExecContext(r.Context(), "INSERT INTO quick_reply(id,organization_id,atajo,texto,created_at,updated_at) VALUES (?,?,?,?,?,?)", id, sesion.OrganizacionID, e.Atajo, e.Texto, ahora, ahora)
	if err != nil {
		if strings.Contains(err.Error(), "1062") {
			responderError(w, 409, "shortcut_exists", "El atajo ya existe", nil)
			return
		}
		s.errorInterno(w, "crear respuesta rápida", err)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "quick-reply.updated", map[string]string{"id": id})
	responderJSON(w, 201, respuestaRapidaDTO{id, e.Atajo, e.Texto, ahora, ahora})
}

func (s *Servidor) actualizarRespuestaRapida(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e struct {
		Atajo string `json:"atajo"`
		Texto string `json:"texto"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	atajo, texto, ok := validarRespuestaRapida(e.Atajo, e.Texto)
	e.Atajo, e.Texto = atajo, texto
	if !ok {
		responderError(w, 422, "validation_error", "Atajo y texto no son válidos", nil)
		return
	}
	res, err := s.db.ExecContext(r.Context(), "UPDATE quick_reply SET atajo=?,texto=?,updated_at=? WHERE organization_id=? AND id=?", e.Atajo, e.Texto, time.Now().UTC(), sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "actualizar respuesta rápida", err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Respuesta no encontrada", nil)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "quick-reply.updated", map[string]string{"id": r.PathValue("id")})
	w.WriteHeader(204)
}

func (s *Servidor) eliminarRespuestaRapida(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM quick_reply WHERE organization_id=? AND id=?", sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "eliminar respuesta rápida", err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Respuesta no encontrada", nil)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "quick-reply.updated", map[string]string{"id": r.PathValue("id")})
	w.WriteHeader(204)
}

type notaDTO struct {
	ID             string
	ConversacionID string
	Texto          string
	AutorID        *string
	Autor          string
	CreadoEn       time.Time
}

func (s *Servidor) listarNotas(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT n.id,n.conversation_id,n.texto,n.author_user_id,COALESCE(u.name,'Usuario eliminado'),n.created_at FROM conversation_note n JOIN conversation cv ON cv.id=n.conversation_id AND cv.organization_id=n.organization_id LEFT JOIN user u ON u.id=n.author_user_id WHERE n.organization_id=? AND n.conversation_id=? ORDER BY n.created_at`, sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "listar notas", err)
		return
	}
	defer rows.Close()
	items := make([]notaDTO, 0)
	for rows.Next() {
		var x notaDTO
		var autor sql.NullString
		if err := rows.Scan(&x.ID, &x.ConversacionID, &x.Texto, &autor, &x.Autor, &x.CreadoEn); err != nil {
			s.errorInterno(w, "leer nota", err)
			return
		}
		if autor.Valid {
			x.AutorID = &autor.String
		}
		items = append(items, x)
	}
	responderJSON(w, 200, map[string]any{"items": items})
}

func (s *Servidor) crearNota(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e struct {
		Texto string `json:"texto"`
	}
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.Texto = strings.TrimSpace(e.Texto)
	if e.Texto == "" || len([]rune(e.Texto)) > 4000 {
		responderError(w, 422, "validation_error", "La nota debe tener entre 1 y 4000 caracteres", nil)
		return
	}
	var existe bool
	err := s.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM conversation WHERE organization_id=? AND id=?)", sesion.OrganizacionID, r.PathValue("id")).Scan(&existe)
	if err != nil || !existe {
		if errors.Is(err, sql.ErrNoRows) || !existe {
			responderError(w, 404, "not_found", "Conversación no encontrada", nil)
			return
		}
		s.errorInterno(w, "comprobar conversación", err)
		return
	}
	id, _ := identificadores.Nuevo("note")
	ahora := time.Now().UTC()
	_, err = s.db.ExecContext(r.Context(), "INSERT INTO conversation_note(id,organization_id,conversation_id,author_user_id,texto,created_at) VALUES (?,?,?,?,?,?)", id, sesion.OrganizacionID, r.PathValue("id"), sesion.UsuarioID, e.Texto, ahora)
	if err != nil {
		s.errorInterno(w, "crear nota", err)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "note.created", map[string]string{"conversationId": r.PathValue("id"), "noteId": id})
	autorID := sesion.UsuarioID
	responderJSON(w, 201, notaDTO{ID: id, ConversacionID: r.PathValue("id"), Texto: e.Texto, AutorID: &autorID, Autor: sesion.Nombre, CreadoEn: ahora})
}
