package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

type agenteDTO struct {
	ID                 string          `json:"id"`
	Habilitado         bool            `json:"habilitado"`
	Nombre             string          `json:"nombre"`
	Tono               *string         `json:"tono"`
	Instrucciones      *string         `json:"instrucciones"`
	ReglasEscalamiento *string         `json:"reglasEscalamiento"`
	Saludo             *string         `json:"saludo"`
	Rol                string          `json:"rol"`
	Herramientas       json.RawMessage `json:"herramientas"`
}

func (s *Servidor) obtenerAgente(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var a agenteDTO
	var tono, instrucciones, reglas, saludo sql.NullString
	var herramientas []byte
	err := s.db.QueryRowContext(r.Context(), "SELECT id,enabled,name,tone,instructions,escalation_rules,greeting,role,COALESCE(tools,JSON_OBJECT()) FROM agent_profile WHERE organization_id=?", sesion.OrganizacionID).Scan(&a.ID, &a.Habilitado, &a.Nombre, &tono, &instrucciones, &reglas, &saludo, &a.Rol, &herramientas)
	if err != nil {
		s.errorInterno(w, "obtener agente", err)
		return
	}
	a.Tono = nuloString(tono)
	a.Instrucciones = nuloString(instrucciones)
	a.ReglasEscalamiento = nuloString(reglas)
	a.Saludo = nuloString(saludo)
	a.Herramientas = herramientas
	responderJSON(w, http.StatusOK, a)
}

type agenteEntrada struct {
	Habilitado                                                   bool `json:"habilitado"`
	Nombre, Tono, Instrucciones, ReglasEscalamiento, Saludo, Rol string
	Herramientas                                                 map[string]bool `json:"herramientas"`
}

func (s *Servidor) guardarAgente(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e agenteEntrada
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.Nombre = strings.TrimSpace(e.Nombre)
	e.Rol = strings.TrimSpace(e.Rol)
	if e.Nombre == "" || e.Rol == "" {
		responderError(w, 422, "validation_error", "Nombre y rol son obligatorios", nil)
		return
	}
	if e.Habilitado {
		if err := s.comprobarCerebro(r.Context(), sesion.OrganizacionID); err != nil {
			responderError(w, 409, "brain_unavailable", err.Error(), nil)
			return
		}
	}
	herramientas, _ := json.Marshal(e.Herramientas)
	ahora := time.Now().UTC()
	resultado, err := s.db.ExecContext(r.Context(), "UPDATE agent_profile SET enabled=?,name=?,tone=?,instructions=?,escalation_rules=?,greeting=?,role=?,tools=?,updated_at=? WHERE organization_id=?", e.Habilitado, e.Nombre, nuloVacio(e.Tono), nuloVacio(e.Instrucciones), nuloVacio(e.ReglasEscalamiento), nuloVacio(e.Saludo), e.Rol, string(herramientas), ahora, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "guardar agente", err)
		return
	}
	n, _ := resultado.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Perfil de agente no encontrado", nil)
		return
	}
	s.obtenerAgente(w, r)
}

type conocimientoEntrada struct{ Tipo, Pregunta, Respuesta, Contenido string }
type conocimientoDTO struct {
	ID, Tipo                       string
	Pregunta, Respuesta, Contenido *string
	CreadoEn, ActualizadoEn        time.Time
}

func (s *Servidor) listarConocimiento(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,kind,question,answer,content,created_at,updated_at FROM kb_entry WHERE organization_id=? ORDER BY updated_at DESC", sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar conocimiento", err)
		return
	}
	defer rows.Close()
	items := []conocimientoDTO{}
	for rows.Next() {
		var d conocimientoDTO
		var p, a, c sql.NullString
		if err := rows.Scan(&d.ID, &d.Tipo, &p, &a, &c, &d.CreadoEn, &d.ActualizadoEn); err != nil {
			s.errorInterno(w, "leer conocimiento", err)
			return
		}
		d.Pregunta = nuloString(p)
		d.Respuesta = nuloString(a)
		d.Contenido = nuloString(c)
		items = append(items, d)
	}
	responderJSON(w, 200, map[string]any{"items": items})
}

func (s *Servidor) crearConocimiento(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e conocimientoEntrada
	if !decodificarJSON(w, r, &e) {
		return
	}
	if !conocimientoValido(e) {
		responderError(w, 422, "validation_error", "Completa el contenido requerido", nil)
		return
	}
	id, _ := identificadores.Nuevo("kb")
	ahora := time.Now().UTC()
	_, err := s.db.ExecContext(r.Context(), "INSERT INTO kb_entry (id,organization_id,kind,question,answer,content,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", id, sesion.OrganizacionID, e.Tipo, nuloVacio(e.Pregunta), nuloVacio(e.Respuesta), nuloVacio(e.Contenido), ahora, ahora)
	if err != nil {
		s.errorInterno(w, "crear conocimiento", err)
		return
	}
	responderJSON(w, 201, map[string]any{"id": id})
}
func (s *Servidor) actualizarConocimiento(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e conocimientoEntrada
	if !decodificarJSON(w, r, &e) {
		return
	}
	if !conocimientoValido(e) {
		responderError(w, 422, "validation_error", "Completa el contenido requerido", nil)
		return
	}
	resultado, err := s.db.ExecContext(r.Context(), "UPDATE kb_entry SET kind=?,question=?,answer=?,content=?,updated_at=? WHERE organization_id=? AND id=?", e.Tipo, nuloVacio(e.Pregunta), nuloVacio(e.Respuesta), nuloVacio(e.Contenido), time.Now().UTC(), sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "actualizar conocimiento", err)
		return
	}
	n, _ := resultado.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Conocimiento no encontrado", nil)
		return
	}
	w.WriteHeader(204)
}
func (s *Servidor) eliminarConocimiento(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	resultado, err := s.db.ExecContext(r.Context(), "DELETE FROM kb_entry WHERE organization_id=? AND id=?", sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "eliminar conocimiento", err)
		return
	}
	n, _ := resultado.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Conocimiento no encontrado", nil)
		return
	}
	w.WriteHeader(204)
}

func conocimientoValido(e conocimientoEntrada) bool {
	return (e.Tipo == "qa" && strings.TrimSpace(e.Pregunta) != "" && strings.TrimSpace(e.Respuesta) != "") || (e.Tipo == "block" && strings.TrimSpace(e.Contenido) != "")
}
func nuloVacio(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}
func nuloString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	x := v.String
	return &x
}
