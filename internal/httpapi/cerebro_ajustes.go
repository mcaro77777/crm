package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cerebroEstado struct {
	Configurado    bool   `json:"configurado"`
	DispatchURL    string `json:"dispatchURL"`
	HealthURL      string `json:"healthURL"`
	APIKeyUltimos4 string `json:"apiKeyUltimos4"`
}
type cerebroEntrada struct{ DispatchURL, HealthURL, APIKey string }

func (s *Servidor) obtenerCerebro(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	estado := cerebroEstado{DispatchURL: s.leerAjuste(r.Context(), sesion.OrganizacionID, "brain_dispatch_url", ""), HealthURL: s.leerAjuste(r.Context(), sesion.OrganizacionID, "brain_health_url", "")}
	if clave, err := s.leerSecreto(r.Context(), sesion.OrganizacionID, "brain_api_key"); err == nil {
		estado.Configurado = estado.DispatchURL != "" && estado.HealthURL != "" && clave != ""
		if len(clave) >= 4 {
			estado.APIKeyUltimos4 = clave[len(clave)-4:]
		}
	}
	responderJSON(w, 200, estado)
}
func (s *Servidor) guardarCerebro(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e cerebroEntrada
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.DispatchURL = strings.TrimSpace(e.DispatchURL)
	e.HealthURL = strings.TrimSpace(e.HealthURL)
	e.APIKey = strings.TrimSpace(e.APIKey)
	if !urlValida(e.DispatchURL) || !urlValida(e.HealthURL) || len(e.APIKey) < 16 {
		responderError(w, 422, "validation_error", "URLs HTTPS validas y una API key de 16+ caracteres son obligatorias", nil)
		return
	}
	if err := s.guardarAjuste(r.Context(), sesion.OrganizacionID, "brain_dispatch_url", e.DispatchURL); err != nil {
		s.errorInterno(w, "guardar URL de despacho", err)
		return
	}
	if err := s.guardarAjuste(r.Context(), sesion.OrganizacionID, "brain_health_url", e.HealthURL); err != nil {
		s.errorInterno(w, "guardar URL de salud", err)
		return
	}
	if err := s.guardarSecreto(r.Context(), sesion.OrganizacionID, "brain_api_key", e.APIKey); err != nil {
		s.errorInterno(w, "guardar API key", err)
		return
	}
	if err := s.comprobarCerebro(r.Context(), sesion.OrganizacionID); err != nil {
		responderError(w, 422, "brain_unavailable", err.Error(), nil)
		return
	}
	s.obtenerCerebro(w, r)
}
func (s *Servidor) probarCerebro(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if err := s.comprobarCerebro(r.Context(), sesion.OrganizacionID); err != nil {
		responderError(w, 502, "brain_unavailable", err.Error(), nil)
		return
	}
	responderJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Servidor) comprobarCerebro(ctx context.Context, organizacionID string) error {
	salud := s.leerAjuste(ctx, organizacionID, "brain_health_url", "")
	if salud == "" {
		return fmt.Errorf("configura la URL de salud del cerebro")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, salud, nil)
	if err != nil {
		return err
	}
	cliente := &http.Client{Timeout: 8 * time.Second}
	res, err := cliente.Do(req)
	if err != nil {
		return fmt.Errorf("el cerebro no responde: %w", err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("el cerebro respondio %d", res.StatusCode)
	}
	return nil
}
func urlValida(valor string) bool {
	u, err := url.ParseRequestURI(valor)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
