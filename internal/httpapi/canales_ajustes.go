package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crm-api/internal/canales"
	"crm-api/internal/identificadores"
	"crm-api/internal/seguridad"
)

type canalEstado struct {
	Canal, Fuente, CuentaID, Nombre, TokenUltimos4, WebhookURL, VerifyToken string
	Conectado                                                               bool
}

func (c canalEstado) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"canal": c.Canal, "fuente": c.Fuente, "cuentaId": c.CuentaID, "nombre": c.Nombre, "tokenUltimos4": c.TokenUltimos4, "webhookUrl": c.WebhookURL, "verifyToken": c.VerifyToken, "conectado": c.Conectado})
}

type canalEntrada struct {
	Fuente        string `json:"fuente"`
	CuentaID      string `json:"cuentaId"`
	Nombre        string `json:"nombre"`
	Token         string `json:"token"`
	WebhookSecret string `json:"webhookSecret"`
}

type credencialCanal struct{ Canal, Fuente, CuentaID, Nombre, Token, WebhookSecret string }

func (s *Servidor) listarAjustesCanales(w http.ResponseWriter, r *http.Request) {
	org := sesionDe(r.Context()).OrganizacionID
	items := make([]canalEstado, 0, 3)
	for _, canal := range []string{"instagram", "messenger", "tiktok"} {
		estado, err := s.estadoCanal(r.Context(), org, canal)
		if err != nil {
			s.errorInterno(w, "leer canal "+canal, err)
			return
		}
		items = append(items, estado)
	}
	responderJSON(w, 200, map[string]any{"items": items, "capacidades": canales.CatalogoPublico()})
}

func (s *Servidor) estadoCanal(ctx context.Context, org, canal string) (canalEstado, error) {
	e := canalEstado{Canal: canal, Fuente: "meta"}
	if canal == "tiktok" {
		e.Fuente = "zernio"
	}
	cred, err := s.credencialSocial(ctx, org, canal)
	if errors.Is(err, sql.ErrNoRows) {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	e.Conectado, e.Fuente, e.CuentaID, e.Nombre = true, cred.Fuente, cred.CuentaID, cred.Nombre
	if len(cred.Token) >= 4 {
		e.TokenUltimos4 = cred.Token[len(cred.Token)-4:]
	}
	e.VerifyToken = s.tokenWebhook(org)
	ruta := map[string]string{"instagram": "ig", "messenger": "messenger", "tiktok": "tiktok"}[canal]
	e.WebhookURL = strings.TrimRight(s.cfg.URLBase, "/") + "/api/webhooks/" + ruta + "/" + e.VerifyToken
	return e, nil
}

func (s *Servidor) guardarAjustesCanal(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if validarRol(sesion, "owner", "admin") != nil {
		responderError(w, 403, "forbidden", "Solo owner o admin puede configurar canales", nil)
		return
	}
	canal := r.PathValue("canal")
	if canal != "instagram" && canal != "messenger" && canal != "tiktok" {
		responderError(w, 404, "not_found", "Canal desconocido", nil)
		return
	}
	var e canalEntrada
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.Fuente, e.CuentaID, e.Nombre, e.Token, e.WebhookSecret = strings.TrimSpace(e.Fuente), strings.TrimSpace(e.CuentaID), strings.TrimSpace(e.Nombre), strings.TrimSpace(e.Token), strings.TrimSpace(e.WebhookSecret)
	if e.Fuente == "" {
		e.Fuente = "meta"
	}
	if canal == "tiktok" && e.Fuente != "zernio" || canal != "tiktok" && e.Fuente != "meta" && e.Fuente != "zernio" {
		responderError(w, 422, "validation_error", "Fuente no permitida para el canal", nil)
		return
	}
	if e.CuentaID == "" || e.Token == "" || e.Fuente == "zernio" && e.WebhookSecret == "" {
		responderError(w, 422, "validation_error", "Cuenta, token y secreto de webhook son obligatorios para esta fuente", nil)
		return
	}
	cred := credencialCanal{Canal: canal, Fuente: e.Fuente, CuentaID: e.CuentaID, Nombre: e.Nombre, Token: e.Token, WebhookSecret: e.WebhookSecret}
	if err := s.probarCredencialSocial(r.Context(), cred); err != nil {
		responderError(w, 422, "channel_connection_failed", err.Error(), nil)
		return
	}
	if err := s.persistirCredencialSocial(r.Context(), sesion.OrganizacionID, cred); err != nil {
		s.errorInterno(w, "guardar canal", err)
		return
	}
	estado, _ := s.estadoCanal(r.Context(), sesion.OrganizacionID, canal)
	responderJSON(w, 200, estado)
}

func (s *Servidor) probarAjustesCanal(w http.ResponseWriter, r *http.Request) {
	cred, err := s.credencialSocial(r.Context(), sesionDe(r.Context()).OrganizacionID, r.PathValue("canal"))
	if err != nil {
		responderError(w, 409, "channel_not_configured", "Canal no configurado", nil)
		return
	}
	if err = s.probarCredencialSocial(r.Context(), cred); err != nil {
		responderError(w, 502, "channel_connection_failed", err.Error(), nil)
		return
	}
	responderJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Servidor) eliminarAjustesCanal(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if validarRol(sesion, "owner", "admin") != nil {
		responderError(w, 403, "forbidden", "No autorizado", nil)
		return
	}
	tabla, _, _, ok := datosTablaCanal(r.PathValue("canal"))
	if !ok {
		responderError(w, 404, "not_found", "Canal desconocido", nil)
		return
	}
	_, err := s.db.ExecContext(r.Context(), "DELETE FROM "+tabla+" WHERE organization_id=?", sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "eliminar canal", err)
		return
	}
	w.WriteHeader(204)
}

func datosTablaCanal(canal string) (tabla, columnaCuenta, columnaNombre string, ok bool) {
	switch canal {
	case "instagram":
		return "instagram_credentials", "ig_user_id", "username", true
	case "messenger":
		return "messenger_credentials", "page_id", "page_name", true
	case "tiktok":
		return "tiktok_credentials", "account_ref", "username", true
	default:
		return "", "", "", false
	}
}

func (s *Servidor) persistirCredencialSocial(ctx context.Context, org string, c credencialCanal) error {
	tabla, cuenta, nombre, ok := datosTablaCanal(c.Canal)
	if !ok {
		return errors.New("canal invalido")
	}
	token, err := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, c.Token)
	if err != nil {
		return err
	}
	var secreto seguridad.SecretoCifrado
	if c.WebhookSecret != "" {
		secreto, err = seguridad.CifrarSecreto(s.cfg.LlaveCifrado, c.WebhookSecret)
		if err != nil {
			return err
		}
	}
	id, _ := identificadores.Nuevo("chc")
	ahora := time.Now().UTC()
	consulta := fmt.Sprintf("INSERT INTO %s(id,organization_id,source,%s,%s,account_ref,token_cipher,token_iv,token_tag,webhook_secret_cipher,webhook_secret_iv,webhook_secret_tag,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'connected',?,?) ON DUPLICATE KEY UPDATE source=VALUES(source),%s=VALUES(%s),%s=VALUES(%s),account_ref=VALUES(account_ref),token_cipher=VALUES(token_cipher),token_iv=VALUES(token_iv),token_tag=VALUES(token_tag),webhook_secret_cipher=VALUES(webhook_secret_cipher),webhook_secret_iv=VALUES(webhook_secret_iv),webhook_secret_tag=VALUES(webhook_secret_tag),status='connected',updated_at=VALUES(updated_at)", tabla, cuenta, nombre, cuenta, cuenta, nombre, nombre)
	accountRef := any(nil)
	if c.Fuente == "zernio" {
		accountRef = c.CuentaID
	}
	// TikTok ya usa account_ref como su identificador y no puede repetir la columna.
	if c.Canal == "tiktok" {
		consulta = `INSERT INTO tiktok_credentials(id,organization_id,source,account_ref,username,token_cipher,token_iv,token_tag,webhook_secret_cipher,webhook_secret_iv,webhook_secret_tag,status,created_at,updated_at) VALUES (?,?,'zernio',?,?,?,?,?,?,?,?,'connected',?,?) ON DUPLICATE KEY UPDATE username=VALUES(username),token_cipher=VALUES(token_cipher),token_iv=VALUES(token_iv),token_tag=VALUES(token_tag),webhook_secret_cipher=VALUES(webhook_secret_cipher),webhook_secret_iv=VALUES(webhook_secret_iv),webhook_secret_tag=VALUES(webhook_secret_tag),status='connected',updated_at=VALUES(updated_at)`
		_, err = s.db.ExecContext(ctx, consulta, id, org, c.CuentaID, nuloVacio(c.Nombre), token.Cifrado, token.IV, token.Tag, nuloVacio(secreto.Cifrado), nuloVacio(secreto.IV), nuloVacio(secreto.Tag), ahora, ahora)
		return err
	}
	_, err = s.db.ExecContext(ctx, consulta, id, org, c.Fuente, c.CuentaID, nuloVacio(c.Nombre), accountRef, token.Cifrado, token.IV, token.Tag, nuloVacio(secreto.Cifrado), nuloVacio(secreto.IV), nuloVacio(secreto.Tag), ahora, ahora)
	return err
}

func (s *Servidor) credencialSocial(ctx context.Context, org, canal string) (credencialCanal, error) {
	var c credencialCanal
	c.Canal = canal
	tabla, cuenta, nombre, ok := datosTablaCanal(canal)
	if !ok {
		return c, sql.ErrNoRows
	}
	var tokenC, tokenIV, tokenTag string
	var secretoC, secretoIV, secretoTag sql.NullString
	consulta := fmt.Sprintf("SELECT source,%s,COALESCE(%s,''),token_cipher,token_iv,token_tag,webhook_secret_cipher,webhook_secret_iv,webhook_secret_tag FROM %s WHERE organization_id=? AND status='connected'", cuenta, nombre, tabla)
	err := s.db.QueryRowContext(ctx, consulta, org).Scan(&c.Fuente, &c.CuentaID, &c.Nombre, &tokenC, &tokenIV, &tokenTag, &secretoC, &secretoIV, &secretoTag)
	if err != nil {
		return c, err
	}
	c.Token, err = seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: tokenC, IV: tokenIV, Tag: tokenTag})
	if err != nil {
		return c, err
	}
	if secretoC.Valid && secretoIV.Valid && secretoTag.Valid {
		c.WebhookSecret, err = seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: secretoC.String, IV: secretoIV.String, Tag: secretoTag.String})
	}
	return c, err
}

func (s *Servidor) probarCredencialSocial(ctx context.Context, c credencialCanal) error {
	if c.Fuente == "meta" {
		var salida map[string]any
		ruta := url.PathEscape(c.CuentaID) + "?fields=id,name,username"
		return s.clienteMeta(c.Token, "v25.0").Ejecutar(ctx, http.MethodGet, ruta, nil, &salida)
	}
	_, err := s.ejecutarZernio(ctx, c.Token, http.MethodGet, "/accounts/"+url.PathEscape(c.CuentaID), nil)
	return err
}

func (s *Servidor) ejecutarZernio(ctx context.Context, token, metodo, ruta string, entrada any) ([]byte, error) {
	return s.ejecutarZernioConHeaders(ctx, token, metodo, ruta, entrada, nil)
}

func (s *Servidor) ejecutarZernioConHeaders(ctx context.Context, token, metodo, ruta string, entrada any, headers map[string]string) ([]byte, error) {
	var cuerpo io.Reader
	if entrada != nil {
		datos, _ := json.Marshal(entrada)
		cuerpo = bytes.NewReader(datos)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, strings.TrimRight(s.cfg.ZernioBaseURL, "/")+"/"+strings.TrimLeft(ruta, "/"), cuerpo)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for nombre, valor := range headers {
		req.Header.Set(nombre, valor)
	}
	if entrada != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, errors.New("Zernio no respondio")
	}
	defer res.Body.Close()
	datos, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detalle struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(datos, &detalle)
		mensaje := strings.TrimSpace(detalle.Message)
		if mensaje == "" {
			mensaje = strings.TrimSpace(detalle.Error)
		}
		if mensaje == "" {
			mensaje = http.StatusText(res.StatusCode)
		}
		if detalle.Code != "" {
			mensaje += " (" + detalle.Code + ")"
		}
		return nil, fmt.Errorf("Zernio respondio %d: %s", res.StatusCode, mensaje)
	}
	return datos, nil
}

func (s *Servidor) enviarTextoSocial(ctx context.Context, org, canal, destinatario, texto string, etiquetaHumana bool) (string, error) {
	cred, err := s.credencialSocial(ctx, org, canal)
	if err != nil {
		return "", errors.New("canal no configurado")
	}
	if cred.Fuente == "zernio" {
		datos, err := s.ejecutarZernio(ctx, cred.Token, http.MethodPost, "/messages", map[string]any{"account_id": cred.CuentaID, "platform": canal, "recipient": destinatario, "text": texto})
		if err != nil {
			return "", err
		}
		var salida struct {
			ID        string `json:"id"`
			MessageID string `json:"message_id"`
		}
		if json.Unmarshal(datos, &salida) != nil {
			return "", errors.New("Zernio devolvio una respuesta invalida")
		}
		if salida.ID == "" {
			salida.ID = salida.MessageID
		}
		if salida.ID == "" {
			return "", errors.New("Zernio no devolvio el identificador del mensaje")
		}
		return canal + ":" + salida.ID, nil
	}
	payload := map[string]any{"recipient": map[string]string{"id": destinatario}, "message": map[string]string{"text": texto}}
	if canal == "messenger" {
		payload["messaging_type"] = "RESPONSE"
		if etiquetaHumana {
			payload["messaging_type"] = "MESSAGE_TAG"
			payload["tag"] = "HUMAN_AGENT"
		}
	}
	var salida struct {
		MessageID string `json:"message_id"`
	}
	if err := s.clienteMeta(cred.Token, "v25.0").Ejecutar(ctx, http.MethodPost, url.PathEscape(cred.CuentaID)+"/messages", payload, &salida); err != nil {
		return "", err
	}
	if salida.MessageID == "" {
		return "", errors.New("Meta no devolvio el identificador del mensaje")
	}
	return canal + ":" + salida.MessageID, nil
}

func (s *Servidor) enviarTextoWhatsApp(ctx context.Context, org, destinatario, conversacionExterna, texto string) (string, error) {
	if s.fuenteWhatsApp(ctx, org) == "zernio" {
		cred, err := s.credencialWhatsAppZernio(ctx, org)
		if err != nil {
			return "", errors.New("WhatsApp por Zernio no esta configurado")
		}
		if strings.TrimSpace(conversacionExterna) == "" {
			return "", errors.New("la conversacion de Zernio no tiene identificador; espera un nuevo mensaje del cliente")
		}
		clave, _ := identificadores.Nuevo("send")
		ruta := "/inbox/conversations/" + url.PathEscape(conversacionExterna) + "/messages"
		datos, err := s.ejecutarZernioConHeaders(ctx, cred.Token, http.MethodPost, ruta, map[string]any{
			"accountId": cred.CuentaID,
			"message":   texto,
		}, map[string]string{"Idempotency-Key": clave})
		if err != nil {
			return "", err
		}
		var salida struct {
			Data struct {
				MessageID string `json:"messageId"`
			} `json:"data"`
		}
		if json.Unmarshal(datos, &salida) != nil {
			return "", errors.New("Zernio devolvio una respuesta invalida")
		}
		if salida.Data.MessageID == "" {
			return "", errors.New("Zernio no devolvio el identificador del mensaje")
		}
		return "whatsapp:" + salida.Data.MessageID, nil
	}
	cred, err := s.credencialMeta(ctx, org)
	if err != nil {
		return "", errors.New("WhatsApp con Meta no esta configurado")
	}
	return s.clienteMeta(cred.Token, cred.Version).EnviarTexto(ctx, cred.TelefonoID, destinatario, texto)
}
