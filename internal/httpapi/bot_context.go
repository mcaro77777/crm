package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (s *Servidor) contextoBot(w http.ResponseWriter, r *http.Request) {
	org := organizacionBot(r.Context())
	conversacionID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	identidad := strings.TrimSpace(r.URL.Query().Get("identity"))
	if identidad == "" {
		identidad = strings.TrimSpace(r.URL.Query().Get("waIdentity"))
	}
	if conversacionID == "" && identidad == "" {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Indica conversationId o identity", nil)
		return
	}

	var convID, contactoID, nombre, identidadDB, canal string
	var telefono, fichaRaw sql.NullString
	var iaActiva, contactoPausado, prueba bool
	var handoff, ultimoEntrante sql.NullTime
	consulta := `SELECT cv.id,c.id,c.name,c.wa_identity,c.channel,c.phone,c.ficha,c.ai_paused,cv.ai_enabled,cv.handoff_at,cv.last_inbound_at,cv.is_test FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND cv.id=?`
	args := []any{org, conversacionID}
	if conversacionID == "" {
		consulta = `SELECT cv.id,c.id,c.name,c.wa_identity,c.channel,c.phone,c.ficha,c.ai_paused,cv.ai_enabled,cv.handoff_at,cv.last_inbound_at,cv.is_test FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND c.wa_identity=? AND cv.is_test=0`
		args = []any{org, identidad}
	}
	err := s.db.QueryRowContext(r.Context(), consulta, args...).Scan(&convID, &contactoID, &nombre, &identidadDB, &canal, &telefono, &fichaRaw, &contactoPausado, &iaActiva, &handoff, &ultimoEntrante, &prueba)
	if err == sql.ErrNoRows {
		responderError(w, http.StatusNotFound, "not_found", "Conversacion no encontrada", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer contexto del cerebro", err)
		return
	}
	ficha := map[string]any{}
	if fichaRaw.Valid {
		_ = json.Unmarshal([]byte(fichaRaw.String), &ficha)
	}
	var telefonoJSON any
	if telefono.Valid {
		telefonoJSON = telefono.String
	}
	var handoffJSON any
	if handoff.Valid {
		handoffJSON = handoff.Time.UTC()
	}
	ventanaAbierta := prueba
	var restante any
	if !prueba && ultimoEntrante.Valid {
		ms := time.Until(ultimoEntrante.Time.UTC().Add(24 * time.Hour)).Milliseconds()
		if ms > 0 {
			ventanaAbierta = true
			restante = ms
		}
	}
	lead, err := s.leadDelContexto(r, org, contactoID)
	if err != nil {
		s.errorInterno(w, "leer lead para cerebro", err)
		return
	}
	citas, err := s.citasDelContexto(r, org, convID)
	if err != nil {
		s.errorInterno(w, "leer citas para cerebro", err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{
		"contact":      map[string]any{"id": contactoID, "name": nombre, "identity": identidadDB, "waIdentity": identidadDB, "channel": canal, "phone": telefonoJSON, "ficha": ficha},
		"conversation": map[string]any{"id": convID, "aiEnabled": iaActiva && !contactoPausado && !handoff.Valid, "handoffAt": handoffJSON, "windowOpen": ventanaAbierta, "windowRemainingMs": restante},
		"lead":         lead, "appointments": citas, "adOrigen": nil,
	})
}

func (s *Servidor) citasDelContexto(r *http.Request, org, conversacionID string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,scheduled_at,duration_minutes,status,meeting_link,is_test FROM booking WHERE organization_id=? AND conversation_id=? AND status='agendada' AND scheduled_at>=? ORDER BY scheduled_at LIMIT 5`, org, conversacionID, time.Now().UTC().Add(-15*time.Minute))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, estado string
		var inicio time.Time
		var duracion int
		var enlace sql.NullString
		var prueba bool
		if err := rows.Scan(&id, &inicio, &duracion, &estado, &enlace, &prueba); err != nil {
			return nil, err
		}
		var enlaceJSON any
		if enlace.Valid && !prueba {
			enlaceJSON = enlace.String
		}
		items = append(items, map[string]any{"id": id, "scheduledAt": inicio.UTC(), "durationMinutes": duracion, "status": estado, "meetingLink": enlaceJSON, "test": prueba})
	}
	return items, rows.Err()
}

func (s *Servidor) leadDelContexto(r *http.Request, org, contactoID string) (any, error) {
	var id, etapaID, etapaNombre string
	var posicion int
	err := s.db.QueryRowContext(r.Context(), "SELECT l.id,l.stage_id,p.name,p.position FROM `lead` l JOIN pipeline_stage p ON p.id=l.stage_id AND p.organization_id=l.organization_id WHERE l.organization_id=? AND l.contact_id=?", org, contactoID).Scan(&id, &etapaID, &etapaNombre, &posicion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name FROM pipeline_stage WHERE organization_id=? AND kind='open' AND position>? ORDER BY position`, org, posicion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	siguientes := make([]map[string]string, 0)
	for rows.Next() {
		var siguienteID, siguienteNombre string
		if err := rows.Scan(&siguienteID, &siguienteNombre); err != nil {
			return nil, err
		}
		siguientes = append(siguientes, map[string]string{"id": siguienteID, "name": siguienteNombre})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "stageId": etapaID, "stageName": etapaNombre, "nextStages": siguientes}, nil
}
