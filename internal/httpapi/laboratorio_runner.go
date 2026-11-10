package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/lab"
)

// ejecutarCorridaLaboratorio conduce personas guionadas sobre conversaciones
// is_test. El unico saliente permitido es el callback del cerebro por
// /api/bot/messages; enviarTextoConversacion evita que toque Meta.
func (s *Servidor) ejecutarCorridaLaboratorio(ctx context.Context, org, runID string, trabajos []trabajoLaboratorio) {
	for _, trabajo := range trabajos {
		if !s.casoSigueEjecutable(ctx, org, trabajo.CasoID) {
			continue
		}
		if err := s.esperarRespuestaLaboratorio(ctx, org, trabajo.ConversacionID, 0); err != nil {
			s.marcarCasoFallido(ctx, org, trabajo.CasoID, "agent_failed", err)
			continue
		}
		for _, texto := range trabajo.Escenario.Turnos[1:] {
			base, err := s.contarRespuestasLaboratorio(ctx, org, trabajo.ConversacionID)
			if err != nil {
				s.marcarCasoFallido(ctx, org, trabajo.CasoID, "agent_failed", err)
				break
			}
			mensajeID, err := s.registrarTurnoLaboratorio(ctx, org, trabajo.CasoID, trabajo.ConversacionID, texto)
			if err == nil {
				_, err = s.despachos.Encolar(ctx, org, trabajo.ConversacionID, []string{mensajeID}, time.Now().UTC(), true)
			}
			if err == nil {
				err = s.esperarRespuestaLaboratorio(ctx, org, trabajo.ConversacionID, base)
			}
			if err != nil {
				s.marcarCasoFallido(ctx, org, trabajo.CasoID, "agent_failed", err)
				break
			}
		}
		if !s.casoSigueEjecutable(ctx, org, trabajo.CasoID) {
			continue
		}
		if err := s.juzgarCasoLaboratorio(ctx, org, trabajo.CasoID, trabajo.ConversacionID, trabajo.Escenario); err != nil {
			s.marcarCasoFallido(ctx, org, trabajo.CasoID, "judge_failed", err)
		}
	}
	s.finalizarCorridaLaboratorio(ctx, org, runID)
}

func (s *Servidor) casoSigueEjecutable(ctx context.Context, org, casoID string) bool {
	var estado string
	return s.db.QueryRowContext(ctx, `SELECT status FROM agent_test_case WHERE organization_id=? AND id=?`, org, casoID).Scan(&estado) == nil && estado == "running"
}

func (s *Servidor) registrarTurnoLaboratorio(ctx context.Context, org, casoID, conversacionID, texto string) (string, error) {
	texto = string([]rune(texto))
	if texto == "" || len([]rune(texto)) > 4096 {
		return "", errors.New("turno de laboratorio invalido")
	}
	var valido bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_test_case tc JOIN conversation cv ON cv.id=tc.conversation_id AND cv.organization_id=tc.organization_id WHERE tc.organization_id=? AND tc.id=? AND tc.conversation_id=? AND tc.status='running' AND cv.is_test=1 AND cv.ai_enabled=1 AND cv.handoff_at IS NULL)`, org, casoID, conversacionID).Scan(&valido)
	if err != nil || !valido {
		if err != nil {
			return "", err
		}
		return "", errors.New("el caso ya no admite turnos")
	}
	id, err := identificadores.Nuevo("msg")
	if err != nil {
		return "", err
	}
	ahora := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO message(id,organization_id,conversation_id,direction,type,text,status,ai_generated,origin,created_at) VALUES (?,?,?,'in','text',?,'delivered',0,'manual',?)`, id, org, conversacionID, texto, ahora); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversation SET last_inbound_at=?,last_message_at=?,updated_at=? WHERE organization_id=? AND id=? AND is_test=1`, ahora, ahora, ahora, org, conversacionID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.bus.Publicar(org, "message.new", map[string]string{"conversationId": conversacionID, "messageId": id})
	return id, nil
}

func (s *Servidor) contarRespuestasLaboratorio(ctx context.Context, org, conversacionID string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message m JOIN conversation cv ON cv.id=m.conversation_id AND cv.organization_id=m.organization_id WHERE m.organization_id=? AND m.conversation_id=? AND cv.is_test=1 AND m.direction='out' AND m.ai_generated=1`, org, conversacionID).Scan(&total)
	return total, err
}

func (s *Servidor) esperarRespuestaLaboratorio(ctx context.Context, org, conversacionID string, base int) error {
	ctx, cancelar := context.WithTimeout(ctx, esperaRespuestaLaboratorio)
	defer cancelar()
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		total, err := s.contarRespuestasLaboratorio(ctx, org, conversacionID)
		if err != nil {
			return err
		}
		if total > base {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("el cerebro no respondio dentro del tiempo permitido")
		case <-ticker.C:
		}
	}
}

func (s *Servidor) transcripcionLaboratorio(ctx context.Context, org, conversacionID string) ([]lab.MensajeEvaluado, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT direction,COALESCE(text,'') FROM message WHERE organization_id=? AND conversation_id=? AND type='text' ORDER BY created_at,id`, org, conversacionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]lab.MensajeEvaluado, 0)
	for rows.Next() {
		var direccion, texto string
		if err := rows.Scan(&direccion, &texto); err != nil {
			return nil, err
		}
		rol := "cliente"
		if direccion == "out" {
			rol = "agente"
		}
		items = append(items, lab.MensajeEvaluado{Rol: rol, Contenido: texto})
	}
	return items, rows.Err()
}

func (s *Servidor) juzgarCasoLaboratorio(ctx context.Context, org, casoID, conversacionID string, escenario escenarioLaboratorio) error {
	transcripcion, err := s.transcripcionLaboratorio(ctx, org, conversacionID)
	if err != nil {
		return err
	}
	if len(transcripcion) < 2 {
		return errors.New("la conversacion no tiene una respuesta para evaluar")
	}
	juez := lab.NuevoJuez(s.db, s.cfg.LlaveCifrado)
	resultado, err := juez.Evaluar(ctx, org, lab.EntradaJuez{Persona: escenario.Persona, Objetivo: escenario.Objetivo, Criterios: escenario.Criterios, Transcripcion: transcripcion})
	if err != nil {
		return err
	}
	transcriptJSON, _ := json.Marshal(transcripcion)
	hallazgosJSON, _ := json.Marshal(resultado)
	res, err := s.db.ExecContext(ctx, `UPDATE agent_test_case SET transcript=CAST(? AS JSON),veredicto=?,hallazgos=CAST(? AS JSON),status='done' WHERE organization_id=? AND id=? AND conversation_id=? AND status='running'`, string(transcriptJSON), resultado.Veredicto, string(hallazgosJSON), org, casoID, conversacionID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("el caso cambio mientras era evaluado")
	}
	s.bus.Publicar(org, "lab.case.updated", map[string]string{"caseId": casoID, "status": "done"})
	return nil
}

func (s *Servidor) marcarCasoFallido(ctx context.Context, org, casoID, estado string, causa error) {
	if estado != "agent_failed" && estado != "judge_failed" {
		estado = "agent_failed"
	}
	detalle := "fallo no especificado"
	if causa != nil {
		detalle = causa.Error()
	}
	if len([]rune(detalle)) > 500 {
		detalle = string([]rune(detalle)[:500])
	}
	hallazgos, _ := json.Marshal(map[string]string{"error": detalle})
	_, _ = s.db.ExecContext(ctx, `UPDATE agent_test_case SET status=?,hallazgos=CAST(? AS JSON) WHERE organization_id=? AND id=? AND status='running'`, estado, string(hallazgos), org, casoID)
	s.bus.Publicar(org, "lab.case.updated", map[string]string{"caseId": casoID, "status": estado})
}

func (s *Servidor) finalizarCorridaLaboratorio(ctx context.Context, org, runID string) {
	var total, completos, fallidos int
	var promedio sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(status='done'),0),COALESCE(SUM(status IN ('agent_failed','judge_failed')),0),AVG(CASE WHEN status='done' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(hallazgos,'$.puntuacion')) AS DECIMAL(5,2)) END) FROM agent_test_case WHERE organization_id=? AND run_id=?`, org, runID).Scan(&total, &completos, &fallidos, &promedio)
	if err != nil {
		return
	}
	estado, detalle := "done", any(nil)
	if total == 0 || fallidos > 0 || completos != total {
		estado = "failed"
		detalle = fmt.Sprintf("%d de %d casos completados; %d fallaron", completos, total, fallidos)
	}
	var puntuacion any
	if promedio.Valid {
		v := int(promedio.Float64 + 0.5)
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		puntuacion = v
	}
	ahora := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, `UPDATE agent_test_run SET status=?,score=?,error=?,finished_at=? WHERE organization_id=? AND id=? AND status='running'`, estado, puntuacion, detalle, ahora, org, runID)
	s.bus.Publicar(org, "lab.run.updated", map[string]string{"runId": runID, "status": estado})
}

// evaluate permite recuperar una corrida si el proceso se reinicio despues de
// que el cerebro respondiera. No genera mensajes ni reenvia nada.
func (s *Servidor) evaluarCorridaLaboratorio(w http.ResponseWriter, r *http.Request) {
	if err := s.asegurarEsquemaLaboratorio(r.Context()); err != nil {
		s.errorInterno(w, "asegurar laboratorio", err)
		return
	}
	sesion := sesionDe(r.Context())
	runID := r.PathValue("id")
	var estado string
	if err := s.db.QueryRowContext(r.Context(), `SELECT status FROM agent_test_run WHERE organization_id=? AND id=?`, sesion.OrganizacionID, runID).Scan(&estado); errors.Is(err, sql.ErrNoRows) {
		responderError(w, http.StatusNotFound, "not_found", "Corrida no encontrada", nil)
		return
	} else if err != nil {
		s.errorInterno(w, "leer corrida", err)
		return
	}
	if estado != "running" {
		responderError(w, http.StatusConflict, "lab_run_finished", "La corrida ya finalizo", nil)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT tc.id,tc.conversation_id,tc.persona,s.scenario_id,s.nombre,s.objetivo,s.turnos,s.criterios FROM agent_test_case tc JOIN conversation cv ON cv.id=tc.conversation_id AND cv.organization_id=tc.organization_id JOIN lab_case_snapshot s ON s.case_id=tc.id AND s.organization_id=tc.organization_id WHERE tc.organization_id=? AND tc.run_id=? AND tc.status='running' AND cv.is_test=1`, sesion.OrganizacionID, runID)
	if err != nil {
		s.errorInterno(w, "leer casos pendientes", err)
		return
	}
	type pendiente struct {
		caso, conv string
		escenario  escenarioLaboratorio
	}
	pendientes := []pendiente{}
	for rows.Next() {
		var p pendiente
		var turnos, criterios []byte
		if err := rows.Scan(&p.caso, &p.conv, &p.escenario.Persona, &p.escenario.ID, &p.escenario.Nombre, &p.escenario.Objetivo, &turnos, &criterios); err != nil {
			rows.Close()
			s.errorInterno(w, "leer caso pendiente", err)
			return
		}
		if json.Unmarshal(turnos, &p.escenario.Turnos) != nil || json.Unmarshal(criterios, &p.escenario.Criterios) != nil {
			continue
		}
		pendientes = append(pendientes, p)
	}
	rows.Close()
	for _, p := range pendientes {
		err := s.juzgarCasoLaboratorio(r.Context(), sesion.OrganizacionID, p.caso, p.conv, p.escenario)
		if err != nil {
			s.marcarCasoFallido(r.Context(), sesion.OrganizacionID, p.caso, "judge_failed", err)
		}
	}
	s.finalizarCorridaLaboratorio(r.Context(), sesion.OrganizacionID, runID)
	responderJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
