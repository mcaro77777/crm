package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

type cerebroEstado struct {
	Configurado    bool   `json:"configurado"`
	DispatchURL    string `json:"dispatchURL"`
	HealthURL      string `json:"healthURL"`
	APIKeyUltimos4 string `json:"apiKeyUltimos4"`
}

func (s *Servidor) obtenerCerebro(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	estado := cerebroEstado{Configurado: s.comprobarCerebro(r.Context(), sesion.OrganizacionID) == nil}
	responderJSON(w, 200, estado)
}
func (s *Servidor) guardarCerebro(w http.ResponseWriter, r *http.Request) {
	responderError(w, http.StatusGone, "integrated_brain", "El cerebro ahora esta integrado; configura solamente el proveedor de IA", nil)
}
func (s *Servidor) probarCerebro(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if err := s.comprobarCerebro(r.Context(), sesion.OrganizacionID); err != nil {
		responderError(w, http.StatusConflict, "brain_unavailable", err.Error(), nil)
		return
	}
	responderJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Servidor) comprobarCerebro(ctx context.Context, organizacionID string) error {
	var disponible bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM ai_credentials
		WHERE organization_id=? AND status='active'
		AND COALESCE(base_url,'')<>'' AND COALESCE(model,'')<>''
		AND token_cipher IS NOT NULL AND token_iv IS NOT NULL AND token_tag IS NOT NULL
	)`, organizacionID).Scan(&disponible)
	if err != nil {
		return fmt.Errorf("comprobar cerebro integrado: %w", err)
	}
	if !disponible {
		return fmt.Errorf("configura y activa el proveedor de IA")
	}
	return nil
}
func urlValida(valor string) bool {
	u, err := url.ParseRequestURI(valor)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
