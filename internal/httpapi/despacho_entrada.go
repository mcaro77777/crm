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
		JOIN app_setting u ON u.organization_id=cv.organization_id AND u.clave='brain_dispatch_url'
		JOIN app_setting k ON k.organization_id=cv.organization_id AND k.clave='brain_api_key'
		WHERE cv.organization_id=? AND cv.id=? AND cv.is_test=0
		  AND cv.ai_enabled=1 AND cv.handoff_at IS NULL AND c.ai_paused=0
		  AND ap.enabled=1 AND COALESCE(u.valor,'')<>''
		  AND k.secret_cipher IS NOT NULL AND k.secret_iv IS NOT NULL AND k.secret_tag IS NOT NULL
	)`, organizacionID, conversacionID).Scan(&habilitado)
	if err != nil || !habilitado {
		return err
	}
	_, err = s.despachos.Encolar(ctx, organizacionID, conversacionID, []string{mensajeID}, ahora, false)
	return err
}
