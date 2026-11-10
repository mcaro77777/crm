package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

const esperaRespuestaLaboratorio = 90 * time.Second

type entradaCorridaLaboratorio struct {
	EscenarioIDs []string `json:"escenarioIds"`
}

type corridaLaboratorioDTO struct {
	ID             string     `json:"id"`
	Estado         string     `json:"estado"`
	Puntaje        *int       `json:"puntaje"`
	Error          *string    `json:"error"`
	IniciadaEn     time.Time  `json:"iniciadaEn"`
	FinalizadaEn   *time.Time `json:"finalizadaEn"`
	Casos          int        `json:"casos"`
	CasosCompletos int        `json:"casosCompletos"`
	DeltaAnterior  *int       `json:"deltaAnterior,omitempty"`
}

type casoLaboratorioDTO struct {
	ID             string          `json:"id"`
	EscenarioID    string          `json:"escenarioId"`
	Escenario      string          `json:"escenario"`
	Persona        string          `json:"persona"`
	Objetivo       string          `json:"objetivo"`
	Criterios      json.RawMessage `json:"criterios"`
	ConversacionID *string         `json:"conversacionId"`
	Estado         string          `json:"estado"`
	Veredicto      *string         `json:"veredicto"`
	Transcripcion  json.RawMessage `json:"transcripcion"`
	Hallazgos      json.RawMessage `json:"hallazgos"`
	CreadoEn       time.Time       `json:"creadoEn"`
}

type trabajoLaboratorio struct {
	CasoID, ConversacionID, PrimerMensajeID string
	Escenario                               escenarioLaboratorio
}

func (s *Servidor) iniciarCorridaLaboratorio(w http.ResponseWriter, r *http.Request) {
	var entrada entradaCorridaLaboratorio
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	sesion := sesionDe(r.Context())
	if err := s.validarCerebroParaLaboratorio(r.Context(), sesion.OrganizacionID); err != nil {
		responderError(w, http.StatusConflict, "sin_cerebro", err.Error(), nil)
		return
	}
	escenarios, err := s.seleccionarEscenarios(r.Context(), sesion.OrganizacionID, entrada.EscenarioIDs)
	if err != nil {
		s.errorInterno(w, "seleccionar escenarios", err)
		return
	}
	if len(escenarios) == 0 {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Selecciona al menos un escenario activo", nil)
		return
	}

	runID, _ := identificadores.Nuevo("labrun")
	ahora := time.Now().UTC()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.errorInterno(w, "iniciar corrida", err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO agent_test_run(id,organization_id,status,started_at) VALUES (?,?,'running',?)`, runID, sesion.OrganizacionID, ahora); err != nil {
		if esDuplicadoMySQL(err) {
			responderError(w, http.StatusConflict, "lab_run_active", "Ya existe una corrida en curso", nil)
			return
		}
		s.errorInterno(w, "crear corrida", err)
		return
	}
	trabajos := make([]trabajoLaboratorio, 0, len(escenarios))
	for _, escenario := range escenarios {
		trabajo, err := crearCasoLaboratorioTx(r.Context(), tx, sesion.OrganizacionID, runID, escenario, ahora)
		if err != nil {
			s.errorInterno(w, "crear caso de laboratorio", err)
			return
		}
		trabajos = append(trabajos, trabajo)
	}
	if err := tx.Commit(); err != nil {
		s.errorInterno(w, "confirmar corrida", err)
		return
	}

	for i := range trabajos {
		if _, err := s.despachos.Encolar(r.Context(), sesion.OrganizacionID, trabajos[i].ConversacionID, []string{trabajos[i].PrimerMensajeID}, ahora, true); err != nil {
			_, _ = s.db.ExecContext(r.Context(), `UPDATE agent_test_case SET status='agent_failed',hallazgos=JSON_OBJECT('error',?) WHERE organization_id=? AND id=?`, err.Error(), sesion.OrganizacionID, trabajos[i].CasoID)
		}
	}
	s.bus.Publicar(sesion.OrganizacionID, "lab.run.updated", map[string]string{"runId": runID, "status": "running"})
	go s.ejecutarCorridaLaboratorio(context.Background(), sesion.OrganizacionID, runID, trabajos)
	responderJSON(w, http.StatusAccepted, map[string]any{"id": runID, "estado": "running", "casos": len(trabajos)})
}

func crearCasoLaboratorioTx(ctx context.Context, tx *sql.Tx, org, runID string, escenario escenarioLaboratorio, ahora time.Time) (trabajoLaboratorio, error) {
	contactoID, _ := identificadores.Nuevo("ct")
	conversacionID, _ := identificadores.Nuevo("cv")
	casoID, _ := identificadores.Nuevo("labcase")
	mensajeID, _ := identificadores.Nuevo("msg")
	identidad := "lab:" + runID + ":" + escenario.ID
	consultas := []struct {
		q    string
		args []any
	}{
		// El contacto sintetico queda archivado desde su nacimiento: sirve al
		// contrato del despacho, pero nunca contamina Contactos ni el pipeline.
		{`INSERT INTO contact(id,organization_id,channel,wa_identity,name,name_source,source,archived_at,created_at,updated_at) VALUES (?,?,'whatsapp',?,?,'manual','otro',?,?,?)`, []any{contactoID, org, identidad, escenario.Persona, ahora, ahora, ahora}},
		{`INSERT INTO conversation(id,organization_id,contact_id,is_test,channel,ai_enabled,last_inbound_at,last_message_at,unread_count,created_at,updated_at) VALUES (?,?,?,1,'whatsapp',1,?,?,0,?,?)`, []any{conversacionID, org, contactoID, ahora, ahora, ahora, ahora}},
		{`INSERT INTO agent_test_case(id,organization_id,run_id,persona,conversation_id,status,created_at) VALUES (?,?,?,?,?,'running',?)`, []any{casoID, org, runID, escenario.Persona, conversacionID, ahora}},
		{`INSERT INTO lab_case_snapshot(case_id,organization_id,scenario_id,nombre,objetivo,turnos,criterios,created_at) VALUES (?,?,?,?,?,CAST(? AS JSON),CAST(? AS JSON),?)`, []any{casoID, org, escenario.ID, escenario.Nombre, escenario.Objetivo, marshalJSON(escenario.Turnos), marshalJSON(escenario.Criterios), ahora}},
		{`INSERT INTO message(id,organization_id,conversation_id,direction,type,text,status,ai_generated,origin,created_at) VALUES (?,?,?,'in','text',?,'delivered',0,'manual',?)`, []any{mensajeID, org, conversacionID, escenario.Turnos[0], ahora}},
	}
	for _, consulta := range consultas {
		if _, err := tx.ExecContext(ctx, consulta.q, consulta.args...); err != nil {
			return trabajoLaboratorio{}, err
		}
	}
	return trabajoLaboratorio{CasoID: casoID, ConversacionID: conversacionID, PrimerMensajeID: mensajeID, Escenario: escenario}, nil
}

func (s *Servidor) validarCerebroParaLaboratorio(ctx context.Context, org string) error {
	var disponible bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM agent_profile ap
		JOIN app_setting u ON u.organization_id=ap.organization_id AND u.clave='brain_dispatch_url'
		JOIN app_setting k ON k.organization_id=ap.organization_id AND k.clave='brain_api_key'
		WHERE ap.organization_id=? AND ap.enabled=1 AND COALESCE(u.valor,'')<>''
		AND k.secret_cipher IS NOT NULL AND k.secret_iv IS NOT NULL AND k.secret_tag IS NOT NULL
	)`, org).Scan(&disponible)
	if err != nil {
		return err
	}
	if !disponible {
		return errors.New("conecta el cerebro y enciende el agente antes de ejecutar el Laboratorio")
	}
	return nil
}

func (s *Servidor) seleccionarEscenarios(ctx context.Context, org string, ids []string) ([]escenarioLaboratorio, error) {
	seleccion := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			seleccion[id] = struct{}{}
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,nombre,persona,objetivo,turnos,criterios,activo,created_at,updated_at FROM lab_scenario WHERE organization_id=? AND activo=1 ORDER BY updated_at`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultado := make([]escenarioLaboratorio, 0)
	for rows.Next() {
		e, err := escanearEscenario(rows)
		if err != nil {
			return nil, err
		}
		if len(seleccion) == 0 {
			resultado = append(resultado, e)
		} else if _, ok := seleccion[e.ID]; ok {
			resultado = append(resultado, e)
			delete(seleccion, e.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(seleccion) != 0 {
		return nil, fmt.Errorf("uno o mas escenarios no existen o estan inactivos")
	}
	return resultado, nil
}

func (s *Servidor) listarCorridasLaboratorio(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT tr.id,tr.status,tr.score,tr.error,tr.started_at,tr.finished_at,COUNT(tc.id),COALESCE(SUM(tc.status='done'),0) FROM agent_test_run tr LEFT JOIN agent_test_case tc ON tc.run_id=tr.id AND tc.organization_id=tr.organization_id WHERE tr.organization_id=? GROUP BY tr.id,tr.status,tr.score,tr.error,tr.started_at,tr.finished_at ORDER BY tr.started_at DESC LIMIT 100`, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "listar corridas", err)
		return
	}
	defer rows.Close()
	items := make([]corridaLaboratorioDTO, 0)
	for rows.Next() {
		var d corridaLaboratorioDTO
		var puntaje sql.NullInt64
		var detalle sql.NullString
		var fin sql.NullTime
		if err := rows.Scan(&d.ID, &d.Estado, &puntaje, &detalle, &d.IniciadaEn, &fin, &d.Casos, &d.CasosCompletos); err != nil {
			s.errorInterno(w, "leer corrida", err)
			return
		}
		if puntaje.Valid {
			v := int(puntaje.Int64)
			d.Puntaje = &v
		}
		if detalle.Valid {
			v := detalle.String
			d.Error = &v
		}
		d.FinalizadaEn = nuloTiempo(fin)
		items = append(items, d)
	}
	for i := 0; i+1 < len(items); i++ {
		if items[i].Puntaje != nil && items[i+1].Puntaje != nil {
			d := *items[i].Puntaje - *items[i+1].Puntaje
			items[i].DeltaAnterior = &d
		}
	}
	responderJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Servidor) obtenerCorridaLaboratorio(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	runID := r.PathValue("id")
	var corrida corridaLaboratorioDTO
	var puntaje sql.NullInt64
	var detalle sql.NullString
	var fin sql.NullTime
	err := s.db.QueryRowContext(r.Context(), `SELECT id,status,score,error,started_at,finished_at FROM agent_test_run WHERE organization_id=? AND id=?`, sesion.OrganizacionID, runID).Scan(&corrida.ID, &corrida.Estado, &puntaje, &detalle, &corrida.IniciadaEn, &fin)
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, http.StatusNotFound, "not_found", "Corrida no encontrada", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer corrida", err)
		return
	}
	if puntaje.Valid {
		v := int(puntaje.Int64)
		corrida.Puntaje = &v
	}
	if detalle.Valid {
		v := detalle.String
		corrida.Error = &v
	}
	corrida.FinalizadaEn = nuloTiempo(fin)
	casos, err := s.casosDeCorrida(r.Context(), sesion.OrganizacionID, runID)
	if err != nil {
		s.errorInterno(w, "leer casos", err)
		return
	}
	corrida.Casos = len(casos)
	for _, c := range casos {
		if c.Estado == "done" {
			corrida.CasosCompletos++
		}
	}
	responderJSON(w, http.StatusOK, map[string]any{"corrida": corrida, "casos": casos})
}

func (s *Servidor) casosDeCorrida(ctx context.Context, org, runID string) ([]casoLaboratorioDTO, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tc.id,s.scenario_id,s.nombre,tc.persona,s.objetivo,s.criterios,tc.conversation_id,tc.status,tc.veredicto,COALESCE(tc.transcript,JSON_ARRAY()),COALESCE(tc.hallazgos,JSON_OBJECT()),tc.created_at FROM agent_test_case tc JOIN lab_case_snapshot s ON s.case_id=tc.id AND s.organization_id=tc.organization_id WHERE tc.organization_id=? AND tc.run_id=? ORDER BY tc.created_at,tc.id`, org, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]casoLaboratorioDTO, 0)
	for rows.Next() {
		var d casoLaboratorioDTO
		var conv, ver sql.NullString
		var criterios, trans, hall []byte
		if err := rows.Scan(&d.ID, &d.EscenarioID, &d.Escenario, &d.Persona, &d.Objetivo, &criterios, &conv, &d.Estado, &ver, &trans, &hall, &d.CreadoEn); err != nil {
			return nil, err
		}
		if conv.Valid {
			v := conv.String
			d.ConversacionID = &v
		}
		if ver.Valid {
			v := ver.String
			d.Veredicto = &v
		}
		d.Criterios, d.Transcripcion, d.Hallazgos = json.RawMessage(criterios), json.RawMessage(trans), json.RawMessage(hall)
		items = append(items, d)
	}
	return items, rows.Err()
}
