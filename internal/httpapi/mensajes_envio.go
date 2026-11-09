package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/meta"
)

type enviarMensajeBody struct {
	Texto string `json:"texto"`
}

type errorEnvio struct {
	estado          int
	codigo, mensaje string
}

func (e *errorEnvio) Error() string { return e.mensaje }

func (s *Servidor) enviarMensaje(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var entrada enviarMensajeBody
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	mensaje, err := s.enviarTextoConversacion(r.Context(), sesion.OrganizacionID, r.PathValue("id"), entrada.Texto, false)
	if err != nil {
		s.responderErrorEnvio(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, mensaje)
}

func (s *Servidor) mensajeBot(w http.ResponseWriter, r *http.Request) {
	var entrada struct {
		ConversacionID string `json:"conversationId"`
		Texto          string `json:"text"`
	}
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	mensaje, err := s.enviarTextoConversacion(r.Context(), organizacionBot(r.Context()), strings.TrimSpace(entrada.ConversacionID), entrada.Texto, true)
	if err != nil {
		s.responderErrorEnvio(w, err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]string{"messageId": mensaje.ID, "status": mensaje.Estado})
}

func (s *Servidor) enviarTextoConversacion(ctx context.Context, org, conversacionID, texto string, generadoIA bool) (mensajeDTO, error) {
	texto = strings.TrimSpace(texto)
	if conversacionID == "" || texto == "" || len([]rune(texto)) > 4096 {
		return mensajeDTO{}, &errorEnvio{422, "validation_error", "El mensaje debe tener entre 1 y 4096 caracteres"}
	}
	var identidad, canal string
	var ultimoEntrante sql.NullTime
	var prueba, iaActiva, contactoPausado bool
	var handoff sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT c.wa_identity,cv.channel,cv.last_inbound_at,cv.is_test,cv.ai_enabled,cv.handoff_at,c.ai_paused FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND cv.id=?`, org, conversacionID).Scan(&identidad, &canal, &ultimoEntrante, &prueba, &iaActiva, &handoff, &contactoPausado)
	if errors.Is(err, sql.ErrNoRows) {
		return mensajeDTO{}, &errorEnvio{404, "not_found", "Conversacion no encontrada"}
	}
	if err != nil {
		return mensajeDTO{}, err
	}
	if generadoIA && (!iaActiva || handoff.Valid || contactoPausado) {
		return mensajeDTO{}, &errorEnvio{409, "ai_paused", "La conversacion esta a cargo de una persona"}
	}
	if prueba && !generadoIA {
		return mensajeDTO{}, &errorEnvio{409, "sandbox_violation", "Una conversacion de prueba no admite envio manual"}
	}
	if canal != "whatsapp" && !prueba {
		return mensajeDTO{}, &errorEnvio{409, "channel_unsupported", "El canal aun no admite envio"}
	}
	if !prueba && (!ultimoEntrante.Valid || time.Since(ultimoEntrante.Time.UTC()) > 24*time.Hour) {
		return mensajeDTO{}, &errorEnvio{409, "window_closed", "La ventana de WhatsApp esta cerrada"}
	}
	if !generadoIA {
		estado, cambio, err := s.handoffs.Tomar(ctx, org, conversacionID, "manual_reply", time.Now().UTC())
		if err != nil {
			return mensajeDTO{}, err
		}
		s.publicarResponsable(org, conversacionID, estado, cambio)
	}

	var waID any
	if !prueba {
		cred, err := s.credencialMeta(ctx, org)
		if err != nil {
			return mensajeDTO{}, &errorEnvio{409, "whatsapp_not_configured", "Configura WhatsApp antes de enviar"}
		}
		id, err := meta.Nuevo(cred.Token, cred.Version).EnviarTexto(ctx, cred.TelefonoID, identidad, texto)
		if err != nil {
			return mensajeDTO{}, &errorEnvio{502, "meta_send_failed", err.Error()}
		}
		waID = id
	}
	id, err := identificadores.Nuevo("msg")
	if err != nil {
		return mensajeDTO{}, err
	}
	ahora := time.Now().UTC()
	origen := "operator"
	if generadoIA {
		origen = "ai"
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO message (id,organization_id,conversation_id,wa_message_id,direction,type,text,status,ai_generated,origin,created_at) VALUES (?,?,?,?,'out','text',?,'sent',?,?,?)`, id, org, conversacionID, waID, texto, generadoIA, origen, ahora)
	if err != nil {
		return mensajeDTO{}, err
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE conversation SET last_message_at=?,unread_count=0,updated_at=? WHERE organization_id=? AND id=?", ahora, ahora, org, conversacionID)
	s.bus.Publicar(org, "message.new", map[string]string{"conversationId": conversacionID, "messageId": id})
	return mensajeDTO{ID: id, Direccion: "out", Tipo: "text", Texto: sql.NullString{String: texto, Valid: true}, TextoJSON: &texto, Estado: "sent", GeneradoIA: generadoIA, Origen: origen, CreadoEn: ahora}, nil
}

func (s *Servidor) responderErrorEnvio(w http.ResponseWriter, err error) {
	var e *errorEnvio
	if errors.As(err, &e) {
		responderError(w, e.estado, e.codigo, e.mensaje, nil)
		return
	}
	s.errorInterno(w, "enviar mensaje", err)
}

func (s *Servidor) marcarConversacionLeida(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	resultado, err := s.db.ExecContext(r.Context(), "UPDATE conversation SET unread_count=0,updated_at=? WHERE organization_id=? AND id=?", time.Now().UTC(), sesion.OrganizacionID, r.PathValue("id"))
	if err != nil {
		s.errorInterno(w, "marcar conversacion leida", err)
		return
	}
	n, _ := resultado.RowsAffected()
	if n == 0 {
		responderError(w, 404, "not_found", "Conversacion no encontrada", nil)
		return
	}
	s.bus.Publicar(sesion.OrganizacionID, "conversation.read", map[string]string{"conversationId": r.PathValue("id")})
	w.WriteHeader(http.StatusNoContent)
}
