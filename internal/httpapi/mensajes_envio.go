package httpapi

import (
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/meta"
)

type enviarMensajeBody struct {
	Texto string `json:"texto"`
}

func (s *Servidor) enviarMensaje(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var entrada enviarMensajeBody
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.Texto = strings.TrimSpace(entrada.Texto)
	if entrada.Texto == "" || len([]rune(entrada.Texto)) > 4096 {
		responderError(w, 422, "validation_error", "El mensaje debe tener entre 1 y 4096 caracteres", nil)
		return
	}
	var identidad string
	var ultimoEntrante *time.Time
	err := s.db.QueryRowContext(r.Context(), "SELECT c.wa_identity,cv.last_inbound_at FROM conversation cv JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id WHERE cv.organization_id=? AND cv.id=? AND cv.channel='whatsapp' AND cv.is_test=0", sesion.OrganizacionID, r.PathValue("id")).Scan(&identidad, &ultimoEntrante)
	if err != nil {
		s.errorInterno(w, "buscar conversacion para enviar", err)
		return
	}
	if ultimoEntrante == nil || time.Since(ultimoEntrante.UTC()) > 24*time.Hour {
		responderError(w, 409, "window_closed", "La ventana de WhatsApp esta cerrada; envia una plantilla", nil)
		return
	}
	cred, err := s.credencialMeta(r.Context(), sesion.OrganizacionID)
	if err != nil {
		responderError(w, 409, "whatsapp_not_configured", "Configura WhatsApp antes de enviar", nil)
		return
	}
	waID, err := meta.Nuevo(cred.Token, cred.Version).EnviarTexto(r.Context(), cred.TelefonoID, identidad, entrada.Texto)
	if err != nil {
		responderError(w, 502, "meta_send_failed", err.Error(), nil)
		return
	}
	id, _ := identificadores.Nuevo("msg")
	ahora := time.Now().UTC()
	_, err = s.db.ExecContext(r.Context(), "INSERT INTO message (id,organization_id,conversation_id,wa_message_id,direction,type,text,status,ai_generated,origin,created_at) VALUES (?,?,?,?,'out','text',?,'sent',0,'operator',?)", id, sesion.OrganizacionID, r.PathValue("id"), waID, entrada.Texto, ahora)
	if err != nil {
		s.errorInterno(w, "guardar mensaje saliente", err)
		return
	}
	_, _ = s.db.ExecContext(r.Context(), "UPDATE conversation SET last_message_at=?,unread_count=0,updated_at=? WHERE organization_id=? AND id=?", ahora, ahora, sesion.OrganizacionID, r.PathValue("id"))
	s.bus.Publicar(sesion.OrganizacionID, "message.new", map[string]string{"conversationId": r.PathValue("id"), "messageId": id})
	responderJSON(w, http.StatusCreated, map[string]any{"id": id, "direccion": "out", "tipo": "text", "texto": entrada.Texto, "estado": "sent", "generadoIA": false, "origen": "operator", "adjuntoId": nil, "creadoEn": ahora})
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
