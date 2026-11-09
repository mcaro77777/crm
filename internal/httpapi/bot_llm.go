package httpapi

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crm-api/internal/seguridad"
)

func (s *Servidor) proxyLLM(w http.ResponseWriter, r *http.Request) {
	org := organizacionBot(r.Context())
	ruta := strings.Trim(strings.TrimSpace(r.PathValue("ruta")), "/")
	if ruta == "" || strings.Contains(ruta, "..") {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Ruta del proveedor invalida", nil)
		return
	}
	var base, cifrado, iv, tag, estado string
	err := s.db.QueryRowContext(r.Context(), `SELECT base_url,token_cipher,token_iv,token_tag,status FROM ai_credentials WHERE organization_id=?`, org).Scan(&base, &cifrado, &iv, &tag, &estado)
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, http.StatusPaymentRequired, "sin_credencial", "No existe una credencial de IA activa", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer proveedor IA", err)
		return
	}
	if estado != "active" {
		responderError(w, http.StatusPaymentRequired, "sin_credencial", "No existe una credencial de IA activa", nil)
		return
	}
	token, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
	if err != nil {
		s.errorInterno(w, "descifrar proveedor IA", err)
		return
	}
	baseURL, err := url.Parse(strings.TrimRight(base, "/") + "/" + ruta)
	if err != nil || (baseURL.Scheme != "https" && baseURL.Scheme != "http") || baseURL.Host == "" {
		responderError(w, http.StatusPaymentRequired, "sin_credencial", "La URL del proveedor no es valida", nil)
		return
	}
	baseURL.RawQuery = r.URL.RawQuery
	cuerpo, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		responderError(w, http.StatusRequestEntityTooLarge, "body_too_large", "El cuerpo supera 4 MB", nil)
		return
	}
	peticion, err := http.NewRequestWithContext(r.Context(), http.MethodPost, baseURL.String(), bytes.NewReader(cuerpo))
	if err != nil {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "No se pudo construir la peticion", nil)
		return
	}
	peticion.Header.Set("Authorization", "Bearer "+token)
	peticion.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	if peticion.Header.Get("Content-Type") == "" {
		peticion.Header.Set("Content-Type", "application/json")
	}
	cliente := &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	respuesta, err := cliente.Do(peticion)
	if err != nil {
		responderError(w, http.StatusGatewayTimeout, "proveedor_sin_respuesta", "El proveedor de IA no respondio", nil)
		return
	}
	defer respuesta.Body.Close()
	salida, err := io.ReadAll(io.LimitReader(respuesta.Body, (16<<20)+1))
	if err != nil {
		responderError(w, http.StatusBadGateway, "provider_invalid_response", "No se pudo leer la respuesta del proveedor", nil)
		return
	}
	if len(salida) > 16<<20 {
		responderError(w, http.StatusBadGateway, "provider_response_too_large", "La respuesta del proveedor supera 16 MB", nil)
		return
	}
	if respuesta.StatusCode == http.StatusUnauthorized || respuesta.StatusCode == http.StatusPaymentRequired {
		ahora := time.Now().UTC()
		nuevoEstado := "paused_invalid_token"
		if respuesta.StatusCode == http.StatusPaymentRequired {
			nuevoEstado = "paused_no_credit"
		}
		_, _ = s.db.ExecContext(r.Context(), `UPDATE ai_credentials SET status=?,status_reason=?,status_changed_at=?,updated_at=? WHERE organization_id=?`, nuevoEstado, "El proveedor respondio "+respuesta.Status, ahora, ahora, org)
	}
	if tipo := respuesta.Header.Get("Content-Type"); tipo != "" {
		w.Header().Set("Content-Type", tipo)
	}
	w.WriteHeader(respuesta.StatusCode)
	_, _ = w.Write(salida)
}
