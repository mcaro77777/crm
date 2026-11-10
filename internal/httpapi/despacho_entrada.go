package httpapi

import (
	"context"
	"time"
)

// encolarMensajeEntrante aplica todas las guardas antes de abrir el outbox.
// Se llama exclusivamente después de confirmar la transacción del mensaje.
func (s *Servidor) encolarMensajeEntrante(ctx context.Context, organizacionID, conversacionID, mensajeID string, ahora time.Time) error {
	var habilitado bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1
		FROM conversation cv
		JOIN contact c ON c.id=cv.contact_id AND c.organization_id=cv.organization_id
		JOIN agent_profile ap ON ap.organization_id=cv.organization_id
		JOIN ai_credentials ai ON ai.organization_id=cv.organization_id AND ai.status='active'
		WHERE cv.organization_id=? AND cv.id=? AND cv.is_test=0
		  AND cv.ai_enabled=1 AND cv.handoff_at IS NULL AND c.ai_paused=0
		  AND ap.enabled=1 AND COALESCE(ai.base_url,'')<>'' AND COALESCE(ai.model,'')<>''
		  AND ai.token_cipher IS NOT NULL AND ai.token_iv IS NOT NULL AND ai.token_tag IS NOT NULL
	)`, organizacionID, conversacionID).Scan(&habilitado)
	if err != nil || !habilitado {
		return err
	}
	_, err = s.despachos.Encolar(ctx, organizacionID, conversacionID, []string{mensajeID}, ahora, false)
	if err == nil {
		if estado, estadoErr := s.despachos.Estado(ctx, organizacionID, conversacionID); estadoErr == nil && estado != nil {
			s.bus.Publicar(organizacionID, "dispatch.updated", map[string]any{"conversationId": conversacionID, "dispatch": estado})
		}
	}
	return err
}
