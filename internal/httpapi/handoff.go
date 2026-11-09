package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/handoff"
)

type estadoResponsable struct {
	IAHabilitada bool       `json:"iaHabilitada"`
	HandoffEn    *time.Time `json:"handoffEn"`
	Motivo       *string    `json:"motivo"`
	Cambio       bool       `json:"cambio"`
}

func (s *Servidor) handoffBot(w http.ResponseWriter, r *http.Request) {
	var entrada struct {
		ConversacionID string `json:"conversationId"`
		Motivo         string `json:"reason"`
	}
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.ConversacionID = strings.TrimSpace(entrada.ConversacionID)
	entrada.Motivo = strings.TrimSpace(entrada.Motivo)
	if entrada.Motivo == "" {
		entrada.Motivo = "modelo"
	}
	estado, cambio, err := s.handoffs.Tomar(r.Context(), organizacionBot(r.Context()), entrada.ConversacionID, entrada.Motivo, time.Now().UTC())
	if err != nil {
		s.responderErrorHandoff(w, err)
		return
	}
	s.publicarResponsable(organizacionBot(r.Context()), entrada.ConversacionID, estado, cambio)
	responderJSON(w, http.StatusOK, map[string]any{"ok": true, "changed": cambio})
}

func (s *Servidor) cambiarResponsableConversacion(w http.ResponseWriter, r *http.Request) {
	var entrada struct {
		Habilitado *bool `json:"habilitado"`
	}
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	if entrada.Habilitado == nil {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Indica habilitado", nil)
		return
	}
	sesion := sesionDe(r.Context())
	conversacionID := r.PathValue("id")
	ahora := time.Now().UTC()
	var estado handoff.Estado
	var cambio bool
	var err error
	if *entrada.Habilitado {
		if err = s.comprobarCerebro(r.Context(), sesion.OrganizacionID); err != nil {
			responderError(w, http.StatusConflict, "brain_unavailable", err.Error(), nil)
			return
		}
		estado, cambio, err = s.handoffs.Reactivar(r.Context(), sesion.OrganizacionID, conversacionID, ahora)
	} else {
		estado, cambio, err = s.handoffs.Tomar(r.Context(), sesion.OrganizacionID, conversacionID, "manual_reply", ahora)
	}
	if err != nil {
		s.responderErrorHandoff(w, err)
		return
	}
	s.publicarResponsable(sesion.OrganizacionID, conversacionID, estado, cambio)
	responderJSON(w, http.StatusOK, estadoResponsable{IAHabilitada: estado.IAHabilitada, HandoffEn: estado.HandoffEn, Motivo: estado.Motivo, Cambio: cambio})
}

func (s *Servidor) publicarResponsable(org, conversacionID string, estado handoff.Estado, cambio bool) {
	if !cambio {
		return
	}
	s.bus.Publicar(org, "conversation.updated", map[string]any{
		"conversationId": conversacionID,
		"aiEnabled":      estado.IAHabilitada,
		"handoffAt":      estado.HandoffEn,
		"handoffReason":  estado.Motivo,
	})
}

func (s *Servidor) responderErrorHandoff(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, handoff.ErrConversacionNoEncontrada):
		responderError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, handoff.ErrMotivoInvalido):
		responderError(w, http.StatusUnprocessableEntity, "validation_error", err.Error(), nil)
	case errors.Is(err, handoff.ErrContactoPausado):
		responderError(w, http.StatusConflict, "contact_paused", err.Error(), nil)
	case errors.Is(err, handoff.ErrAgenteApagado):
		responderError(w, http.StatusConflict, "agent_disabled", err.Error(), nil)
	default:
		s.errorInterno(w, "cambiar responsable de conversacion", err)
	}
}
