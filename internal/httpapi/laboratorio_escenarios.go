package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

// registrarRutasLaboratorio mantiene toda la superficie de E1 en un modulo.
// El constructor HTTP solo necesita invocar esta funcion una vez.
func (s *Servidor) registrarRutasLaboratorio(mux *http.ServeMux) {
	mux.Handle("GET /api/lab/scenarios", s.requerirSesion(http.HandlerFunc(s.listarEscenariosLaboratorio)))
	mux.Handle("POST /api/lab/scenarios", s.requerirSesion(http.HandlerFunc(s.crearEscenarioLaboratorio)))
	mux.Handle("PATCH /api/lab/scenarios/{id}", s.requerirSesion(http.HandlerFunc(s.actualizarEscenarioLaboratorio)))
	mux.Handle("DELETE /api/lab/scenarios/{id}", s.requerirSesion(http.HandlerFunc(s.desactivarEscenarioLaboratorio)))
	mux.Handle("GET /api/lab/runs", s.requerirSesion(http.HandlerFunc(s.listarCorridasLaboratorio)))
	mux.Handle("POST /api/lab/runs", s.requerirSesion(http.HandlerFunc(s.iniciarCorridaLaboratorio)))
	mux.Handle("GET /api/lab/compare", s.requerirSesion(http.HandlerFunc(s.compararCorridasLaboratorio)))
	mux.Handle("GET /api/lab/runs/{id}", s.requerirSesion(http.HandlerFunc(s.obtenerCorridaLaboratorio)))
	mux.Handle("POST /api/lab/runs/{id}/evaluate", s.requerirSesion(http.HandlerFunc(s.evaluarCorridaLaboratorio)))
}

func (s *Servidor) listarEscenariosLaboratorio(w http.ResponseWriter, r *http.Request) {
	if err := s.asegurarEsquemaLaboratorio(r.Context()); err != nil {
		s.errorInterno(w, "asegurar laboratorio", err)
		return
	}
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,nombre,persona,objetivo,turnos,criterios,activo,created_at,updated_at FROM lab_scenario WHERE organization_id=? ORDER BY activo DESC,updated_at DESC`, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar escenarios", err)
		return
	}
	defer rows.Close()
	items := make([]escenarioLaboratorio, 0)
	for rows.Next() {
		e, err := escanearEscenario(rows)
		if err != nil {
			s.errorInterno(w, "leer escenario", err)
			return
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		s.errorInterno(w, "recorrer escenarios", err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Servidor) crearEscenarioLaboratorio(w http.ResponseWriter, r *http.Request) {
	if err := s.asegurarEsquemaLaboratorio(r.Context()); err != nil {
		s.errorInterno(w, "asegurar laboratorio", err)
		return
	}
	var entrada entradaEscenarioLaboratorio
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada, err := normalizarEscenario(entrada)
	if err != nil {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", err.Error(), nil)
		return
	}
	id, err := identificadores.Nuevo("labsc")
	if err != nil {
		s.errorInterno(w, "crear identificador de escenario", err)
		return
	}
	activo := true
	if entrada.Activo != nil {
		activo = *entrada.Activo
	}
	ahora := time.Now().UTC()
	sesion := sesionDe(r.Context())
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO lab_scenario(id,organization_id,nombre,persona,objetivo,turnos,criterios,activo,created_at,updated_at) VALUES (?,?,?,?,?,CAST(? AS JSON),CAST(? AS JSON),?,?,?)`, id, sesion.OrganizacionID, entrada.Nombre, entrada.Persona, entrada.Objetivo, marshalJSON(entrada.Turnos), marshalJSON(entrada.Criterios), activo, ahora, ahora)
	if err != nil {
		s.errorInterno(w, "crear escenario", err)
		return
	}
	e, err := s.cargarEscenario(r.Context(), sesion.OrganizacionID, id)
	if err != nil {
		s.errorInterno(w, "leer escenario creado", err)
		return
	}
	responderJSON(w, http.StatusCreated, e)
}

func (s *Servidor) actualizarEscenarioLaboratorio(w http.ResponseWriter, r *http.Request) {
	if err := s.asegurarEsquemaLaboratorio(r.Context()); err != nil {
		s.errorInterno(w, "asegurar laboratorio", err)
		return
	}
	var entrada entradaEscenarioLaboratorio
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada, err := normalizarEscenario(entrada)
	if err != nil {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", err.Error(), nil)
		return
	}
	sesion := sesionDe(r.Context())
	actual, err := s.cargarEscenario(r.Context(), sesion.OrganizacionID, strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, http.StatusNotFound, "not_found", "Escenario no encontrado", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer escenario", err)
		return
	}
	activo := actual.Activo
	if entrada.Activo != nil {
		activo = *entrada.Activo
	}
	_, err = s.db.ExecContext(r.Context(), `UPDATE lab_scenario SET nombre=?,persona=?,objetivo=?,turnos=CAST(? AS JSON),criterios=CAST(? AS JSON),activo=?,updated_at=? WHERE organization_id=? AND id=?`, entrada.Nombre, entrada.Persona, entrada.Objetivo, marshalJSON(entrada.Turnos), marshalJSON(entrada.Criterios), activo, time.Now().UTC(), sesion.OrganizacionID, actual.ID)
	if err != nil {
		s.errorInterno(w, "actualizar escenario", err)
		return
	}
	e, err := s.cargarEscenario(r.Context(), sesion.OrganizacionID, actual.ID)
	if err != nil {
		s.errorInterno(w, "leer escenario actualizado", err)
		return
	}
	responderJSON(w, http.StatusOK, e)
}

// Los escenarios se conservan para que una corrida historica siga siendo
// interpretable. DELETE los desactiva en vez de borrar evidencia.
func (s *Servidor) desactivarEscenarioLaboratorio(w http.ResponseWriter, r *http.Request) {
	if err := s.asegurarEsquemaLaboratorio(r.Context()); err != nil {
		s.errorInterno(w, "asegurar laboratorio", err)
		return
	}
	sesion := sesionDe(r.Context())
	res, err := s.db.ExecContext(r.Context(), `UPDATE lab_scenario SET activo=0,updated_at=? WHERE organization_id=? AND id=?`, time.Now().UTC(), sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "desactivar escenario", err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		responderError(w, http.StatusNotFound, "not_found", "Escenario no encontrado", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
