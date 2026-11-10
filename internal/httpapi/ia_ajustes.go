package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/seguridad"
)

type iaEstado struct {
	Configurada   bool   `json:"configurada"`
	Proveedor     string `json:"proveedor"`
	BaseURL       string `json:"baseURL"`
	Modelo        string `json:"modelo"`
	TokenUltimos4 string `json:"tokenUltimos4"`
	Estado        string `json:"estado"`
}
type iaEntrada struct{ Proveedor, BaseURL, Modelo, Token string }

func (s *Servidor) obtenerIA(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	estado, err := s.estadoIA(r.Context(), sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "leer IA", err)
		return
	}
	responderJSON(w, 200, estado)
}
func (s *Servidor) estadoIA(ctx context.Context, org string) (iaEstado, error) {
	var e iaEstado
	err := s.db.QueryRowContext(ctx, "SELECT provider,base_url,model,token_last4,status FROM ai_credentials WHERE organization_id=?", org).Scan(&e.Proveedor, &e.BaseURL, &e.Modelo, &e.TokenUltimos4, &e.Estado)
	if err == sql.ErrNoRows {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	// Una credencial pausada o rechazada no deja listo al agente ni al juez del
	// Laboratorio, aunque sus datos sigan almacenados para poder corregirlos.
	e.Configurada = e.Estado == "active"
	return e, nil
}
func (s *Servidor) guardarIA(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var e iaEntrada
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.Proveedor = strings.TrimSpace(e.Proveedor)
	e.BaseURL = strings.TrimRight(strings.TrimSpace(e.BaseURL), "/")
	e.Modelo = strings.TrimSpace(e.Modelo)
	e.Token = strings.TrimSpace(e.Token)
	if (e.Proveedor != "openrouter" && e.Proveedor != "openai_compatible") || !urlValida(e.BaseURL) || e.Modelo == "" || e.Token == "" {
		responderError(w, 422, "validation_error", "Proveedor, URL, modelo y token son obligatorios", nil)
		return
	}
	if err := probarProveedorIA(r.Context(), e.BaseURL, e.Token, e.Modelo); err != nil {
		responderError(w, 422, "ai_connection_failed", err.Error(), nil)
		return
	}
	secreto, err := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, e.Token)
	if err != nil {
		s.errorInterno(w, "cifrar token IA", err)
		return
	}
	id, _ := identificadores.Nuevo("aic")
	last4 := e.Token
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	ahora := time.Now().UTC()
	resultado, err := s.db.ExecContext(r.Context(), "UPDATE ai_credentials SET provider=?,base_url=?,model=?,token_cipher=?,token_iv=?,token_tag=?,token_last4=?,status='active',status_reason=NULL,status_changed_at=?,updated_at=? WHERE organization_id=?", e.Proveedor, e.BaseURL, e.Modelo, secreto.Cifrado, secreto.IV, secreto.Tag, last4, ahora, ahora, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "actualizar IA", err)
		return
	}
	n, _ := resultado.RowsAffected()
	if n == 0 {
		_, err = s.db.ExecContext(r.Context(), "INSERT INTO ai_credentials (id,organization_id,provider,base_url,model,token_cipher,token_iv,token_tag,token_last4,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,'active',?,?)", id, sesion.OrganizacionID, e.Proveedor, e.BaseURL, e.Modelo, secreto.Cifrado, secreto.IV, secreto.Tag, last4, ahora, ahora)
		if err != nil {
			s.errorInterno(w, "guardar IA", err)
			return
		}
	}
	estado, _ := s.estadoIA(r.Context(), sesion.OrganizacionID)
	responderJSON(w, 200, estado)
}
func (s *Servidor) probarIA(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	var base, modelo, cifrado, iv, tag string
	err := s.db.QueryRowContext(r.Context(), "SELECT base_url,model,token_cipher,token_iv,token_tag FROM ai_credentials WHERE organization_id=?", sesion.OrganizacionID).Scan(&base, &modelo, &cifrado, &iv, &tag)
	if err != nil {
		responderError(w, 409, "ai_not_configured", "Configura la IA primero", nil)
		return
	}
	token, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
	if err != nil {
		s.errorInterno(w, "descifrar IA", err)
		return
	}
	if err := probarProveedorIA(r.Context(), base, token, modelo); err != nil {
		responderError(w, 502, "ai_connection_failed", err.Error(), nil)
		return
	}
	responderJSON(w, 200, map[string]bool{"ok": true})
}
func probarProveedorIA(ctx context.Context, base, token, modelo string) error {
	u, err := url.Parse(strings.TrimRight(base, "/") + "/models")
	if err != nil {
		return fmt.Errorf("la URL base no es válida")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo contactar al proveedor: %w", err)
	}
	defer res.Body.Close()
	cuerpo, err := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if err != nil {
		return fmt.Errorf("no se pudo leer la respuesta del proveedor")
	}
	if len(cuerpo) > 16<<20 {
		return fmt.Errorf("la respuesta del proveedor es demasiado grande")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var respuestaError struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(cuerpo, &respuestaError)
		mensaje := strings.TrimSpace(respuestaError.Error.Message)
		if mensaje == "" {
			mensaje = http.StatusText(res.StatusCode)
		}
		return fmt.Errorf("el proveedor rechazó la conexión (%d): %s", res.StatusCode, mensaje)
	}
	var modelos struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(cuerpo, &modelos); err != nil {
		return fmt.Errorf("el proveedor respondió, pero su listado de modelos no tiene el formato esperado")
	}
	for _, disponible := range modelos.Data {
		if disponible.ID == modelo {
			return nil
		}
	}
	return fmt.Errorf("la conexión funciona, pero el modelo %q no está disponible para esta API key", modelo)
}
