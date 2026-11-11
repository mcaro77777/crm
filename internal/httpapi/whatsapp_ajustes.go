package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/meta"
	"crm-api/internal/seguridad"
)

type whatsappEntrada struct {
	Fuente     string `json:"fuente"`
	WABAID     string `json:"wabaId"`
	TelefonoID string `json:"phoneNumberId"`
	CuentaID   string `json:"cuentaId"`
	Nombre     string `json:"nombre"`
	Token      string `json:"token"`
	AppSecret  string `json:"appSecret"`
	Version    string `json:"graphApiVersion"`
}

type whatsappEstado struct {
	Conectado         bool   `json:"conectado"`
	Fuente            string `json:"fuente"`
	WABAID            string `json:"wabaId,omitempty"`
	TelefonoID        string `json:"phoneNumberId,omitempty"`
	CuentaID          string `json:"cuentaId,omitempty"`
	Telefono          string `json:"telefono,omitempty"`
	NombreVerificado  string `json:"nombreVerificado,omitempty"`
	TokenUltimos4     string `json:"tokenUltimos4,omitempty"`
	Version           string `json:"graphApiVersion"`
	WebhookURL        string `json:"webhookUrl,omitempty"`
	VerifyToken       string `json:"verifyToken,omitempty"`
	WebhookGestionado bool   `json:"webhookGestionado,omitempty"`
}

type cuentaWhatsAppZernio struct {
	ID, Nombre, Usuario, Estado string
}

func (c cuentaWhatsAppZernio) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"id": c.ID, "nombre": c.Nombre, "usuario": c.Usuario, "estado": c.Estado})
}

func (s *Servidor) obtenerAjustesWhatsApp(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	estado, err := s.estadoWhatsApp(r.Context(), sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "leer ajustes de WhatsApp", err)
		return
	}
	responderJSON(w, http.StatusOK, estado)
}

func (s *Servidor) estadoWhatsApp(ctx context.Context, organizacionID string) (whatsappEstado, error) {
	estado := whatsappEstado{Fuente: s.fuenteWhatsApp(ctx, organizacionID), Version: s.leerAjuste(ctx, organizacionID, "meta_graph_version", "v25.0")}
	if estado.Fuente == "zernio" {
		cred, err := s.credencialWhatsAppZernio(ctx, organizacionID)
		if errors.Is(err, sql.ErrNoRows) {
			return estado, nil
		}
		if err != nil {
			return estado, err
		}
		estado.Conectado, estado.CuentaID, estado.NombreVerificado = true, cred.CuentaID, cred.Nombre
		if len(cred.Token) >= 4 {
			estado.TokenUltimos4 = cred.Token[len(cred.Token)-4:]
		}
		estado.VerifyToken = s.tokenWebhook(organizacionID)
		estado.WebhookURL = strings.TrimRight(s.cfg.URLBase, "/") + "/api/webhooks/wa/" + estado.VerifyToken
		estado.WebhookGestionado = s.leerAjuste(ctx, organizacionID, "whatsapp_zernio_webhook_id", "") != ""
		return estado, nil
	}
	var cifrado, iv, tag string
	err := s.db.QueryRowContext(ctx, "SELECT waba_id,phone_number_id,COALESCE(display_phone_number,''),COALESCE(verified_name,''),token_cipher,token_iv,token_tag FROM meta_credentials WHERE organization_id=?", organizacionID).Scan(&estado.WABAID, &estado.TelefonoID, &estado.Telefono, &estado.NombreVerificado, &cifrado, &iv, &tag)
	if err == sql.ErrNoRows {
		return estado, nil
	}
	if err != nil {
		return estado, err
	}
	token, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
	if err != nil {
		return estado, err
	}
	estado.Conectado = true
	if len(token) >= 4 {
		estado.TokenUltimos4 = token[len(token)-4:]
	}
	verify := s.tokenWebhook(organizacionID)
	estado.VerifyToken = verify
	estado.WebhookURL = strings.TrimRight(s.cfg.URLBase, "/") + "/api/webhooks/wa/" + verify
	return estado, nil
}

func (s *Servidor) guardarAjustesWhatsApp(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if validarRol(sesion, "owner", "admin") != nil {
		responderError(w, http.StatusForbidden, "forbidden", "Solo owner o admin puede configurar WhatsApp", nil)
		return
	}
	var entrada whatsappEntrada
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.Fuente = strings.ToLower(strings.TrimSpace(entrada.Fuente))
	if entrada.Fuente == "" {
		entrada.Fuente = "meta"
	}
	entrada.WABAID = strings.TrimSpace(entrada.WABAID)
	entrada.TelefonoID = strings.TrimSpace(entrada.TelefonoID)
	entrada.CuentaID = strings.TrimSpace(entrada.CuentaID)
	entrada.Nombre = strings.TrimSpace(entrada.Nombre)
	entrada.Token = strings.TrimSpace(entrada.Token)
	entrada.AppSecret = strings.TrimSpace(entrada.AppSecret)
	entrada.Version = strings.TrimSpace(entrada.Version)
	if entrada.Fuente != "meta" && entrada.Fuente != "zernio" {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Selecciona Meta oficial o Zernio", nil)
		return
	}
	if entrada.Fuente == "zernio" {
		s.guardarWhatsAppZernio(w, r, sesion.OrganizacionID, entrada)
		return
	}
	if entrada.WABAID == "" || entrada.TelefonoID == "" || entrada.Token == "" {
		responderError(w, 422, "validation_error", "WABA ID, Phone Number ID y token son obligatorios", nil)
		return
	}
	if entrada.Version == "" {
		entrada.Version = "v25.0"
	}
	telefono, err := s.clienteMeta(entrada.Token, entrada.Version).ConsultarTelefono(r.Context(), entrada.TelefonoID)
	if err != nil {
		responderError(w, 422, "meta_connection_failed", err.Error(), nil)
		return
	}
	secreto, err := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, entrada.Token)
	if err != nil {
		s.errorInterno(w, "cifrar token de Meta", err)
		return
	}
	id, _ := identificadores.Nuevo("mc")
	ahora := time.Now().UTC()
	var perteneceAOtra int
	if err = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM meta_credentials WHERE phone_number_id=? AND organization_id<>?", entrada.TelefonoID, sesion.OrganizacionID).Scan(&perteneceAOtra); err != nil {
		s.errorInterno(w, "comprobar numero de Meta", err)
		return
	}
	if perteneceAOtra > 0 {
		responderError(w, http.StatusConflict, "phone_already_connected", "Este Phone Number ID ya pertenece a otra organizacion", nil)
		return
	}
	resultado, err := s.db.ExecContext(r.Context(), `UPDATE meta_credentials SET waba_id=?,phone_number_id=?,display_phone_number=?,verified_name=?,token_cipher=?,token_iv=?,token_tag=?,status='connected',updated_at=? WHERE organization_id=?`, entrada.WABAID, entrada.TelefonoID, telefono.DisplayPhoneNumber, telefono.VerifiedName, secreto.Cifrado, secreto.IV, secreto.Tag, ahora, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "guardar credenciales de Meta", err)
		return
	}
	actualizadas, _ := resultado.RowsAffected()
	if actualizadas == 0 {
		_, err = s.db.ExecContext(r.Context(), `INSERT INTO meta_credentials (id,organization_id,waba_id,phone_number_id,display_phone_number,verified_name,token_cipher,token_iv,token_tag,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,'connected',?,?)`, id, sesion.OrganizacionID, entrada.WABAID, entrada.TelefonoID, telefono.DisplayPhoneNumber, telefono.VerifiedName, secreto.Cifrado, secreto.IV, secreto.Tag, ahora, ahora)
		if err != nil {
			s.errorInterno(w, "insertar credenciales de Meta", err)
			return
		}
	}
	if err := s.guardarAjuste(r.Context(), sesion.OrganizacionID, "meta_graph_version", entrada.Version); err != nil {
		s.errorInterno(w, "guardar version de Meta", err)
		return
	}
	if entrada.AppSecret != "" {
		if err := s.guardarSecreto(r.Context(), sesion.OrganizacionID, "meta_app_secret", entrada.AppSecret); err != nil {
			s.errorInterno(w, "guardar secreto de Meta", err)
			return
		}
	}
	if err := s.guardarAjuste(r.Context(), sesion.OrganizacionID, "whatsapp_source", "meta"); err != nil {
		s.errorInterno(w, "guardar transporte de WhatsApp", err)
		return
	}
	estado, _ := s.estadoWhatsApp(r.Context(), sesion.OrganizacionID)
	responderJSON(w, http.StatusOK, estado)
}

func (s *Servidor) probarWhatsApp(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if s.fuenteWhatsApp(r.Context(), sesion.OrganizacionID) == "zernio" {
		cred, err := s.credencialWhatsAppZernio(r.Context(), sesion.OrganizacionID)
		if err != nil {
			responderError(w, http.StatusConflict, "whatsapp_not_configured", "WhatsApp por Zernio no esta configurado", nil)
			return
		}
		if _, err = s.comprobarCuentaWhatsAppZernio(r.Context(), cred.Token, cred.CuentaID); err != nil {
			responderError(w, http.StatusBadGateway, "zernio_connection_failed", err.Error(), nil)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"ok": true, "fuente": "zernio", "nombreVerificado": cred.Nombre})
		return
	}
	cred, err := s.credencialMeta(r.Context(), sesion.OrganizacionID)
	if err != nil {
		responderError(w, 409, "whatsapp_not_configured", "WhatsApp no esta configurado", nil)
		return
	}
	telefono, err := s.clienteMeta(cred.Token, cred.Version).ConsultarTelefono(r.Context(), cred.TelefonoID)
	if err != nil {
		responderError(w, 502, "meta_connection_failed", err.Error(), nil)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"ok": true, "telefono": telefono.DisplayPhoneNumber, "nombreVerificado": telefono.VerifiedName})
}

func (s *Servidor) eliminarAjustesWhatsApp(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if validarRol(sesion, "owner", "admin") != nil {
		responderError(w, http.StatusForbidden, "forbidden", "No autorizado", nil)
		return
	}
	if s.fuenteWhatsApp(r.Context(), sesion.OrganizacionID) == "zernio" {
		if cred, err := s.credencialWhatsAppZernio(r.Context(), sesion.OrganizacionID); err == nil {
			if webhookID := s.leerAjuste(r.Context(), sesion.OrganizacionID, "whatsapp_zernio_webhook_id", ""); webhookID != "" {
				if _, err = s.ejecutarZernio(r.Context(), cred.Token, http.MethodDelete, "/webhooks/settings?webhookId="+url.QueryEscape(webhookID), nil); err != nil {
					responderError(w, http.StatusBadGateway, "zernio_webhook_delete_failed", "Zernio no permitió eliminar el webhook. Intenta nuevamente para evitar que siga enviando eventos.", nil)
					return
				}
			}
		}
	}
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM app_setting WHERE organization_id=? AND clave IN ('whatsapp_source','whatsapp_zernio_account_id','whatsapp_zernio_name','whatsapp_zernio_token','whatsapp_zernio_webhook_secret','whatsapp_zernio_webhook_id')`, sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "eliminar credenciales de Zernio", err)
		return
	}
	_, err = s.db.ExecContext(r.Context(), "DELETE FROM meta_credentials WHERE organization_id=?", sesion.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "eliminar credenciales de Meta", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type credencialMeta struct{ Token, TelefonoID, WABAID, Version string }

func (s *Servidor) clienteMeta(token, version string) *meta.Cliente {
	return meta.NuevoConBaseURL(token, version, s.cfg.MetaGraphBaseURL)
}

func (s *Servidor) credencialMeta(ctx context.Context, organizacionID string) (credencialMeta, error) {
	var c credencialMeta
	if s.fuenteWhatsApp(ctx, organizacionID) != "meta" {
		return c, sql.ErrNoRows
	}
	var cifrado, iv, tag string
	err := s.db.QueryRowContext(ctx, "SELECT waba_id,phone_number_id,token_cipher,token_iv,token_tag FROM meta_credentials WHERE organization_id=?", organizacionID).Scan(&c.WABAID, &c.TelefonoID, &cifrado, &iv, &tag)
	if err != nil {
		return c, err
	}
	c.Token, err = seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
	c.Version = s.leerAjuste(ctx, organizacionID, "meta_graph_version", "v25.0")
	return c, err
}

func (s *Servidor) fuenteWhatsApp(ctx context.Context, org string) string {
	fuente := strings.ToLower(strings.TrimSpace(s.leerAjuste(ctx, org, "whatsapp_source", "meta")))
	if fuente != "zernio" {
		return "meta"
	}
	return fuente
}

func (s *Servidor) credencialWhatsAppZernio(ctx context.Context, org string) (credencialCanal, error) {
	c := credencialCanal{Canal: "whatsapp", Fuente: "zernio", CuentaID: s.leerAjuste(ctx, org, "whatsapp_zernio_account_id", ""), Nombre: s.leerAjuste(ctx, org, "whatsapp_zernio_name", "")}
	if c.CuentaID == "" {
		return c, sql.ErrNoRows
	}
	var err error
	c.Token, err = s.leerSecreto(ctx, org, "whatsapp_zernio_token")
	if err != nil {
		return c, err
	}
	c.WebhookSecret, err = s.leerSecreto(ctx, org, "whatsapp_zernio_webhook_secret")
	return c, err
}

func (s *Servidor) guardarWhatsAppZernio(w http.ResponseWriter, r *http.Request, org string, e whatsappEntrada) {
	if e.CuentaID == "" || e.Token == "" {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Selecciona una cuenta de WhatsApp e ingresa el token de Zernio", nil)
		return
	}
	cuenta, err := s.comprobarCuentaWhatsAppZernio(r.Context(), e.Token, e.CuentaID)
	if err != nil {
		responderError(w, http.StatusUnprocessableEntity, "zernio_connection_failed", err.Error(), nil)
		return
	}
	if e.Nombre == "" {
		e.Nombre = cuenta.Nombre
	}
	webhookID := s.leerAjuste(r.Context(), org, "whatsapp_zernio_webhook_id", "")
	secreto := ""
	if webhookID != "" {
		secreto, _ = s.leerSecreto(r.Context(), org, "whatsapp_zernio_webhook_secret")
	}
	if secreto == "" {
		bytesSecreto := make([]byte, 32)
		if _, err = rand.Read(bytesSecreto); err != nil {
			s.errorInterno(w, "generar secreto de Zernio", err)
			return
		}
		secreto = hex.EncodeToString(bytesSecreto)
	}
	// La fuente activa se cambia al final: si el guardado se interrumpe, la
	// conexión anterior continúa operativa en lugar de quedar a medio configurar.
	for _, ajuste := range []struct{ clave, valor string }{
		{"whatsapp_zernio_account_id", e.CuentaID},
		{"whatsapp_zernio_name", e.Nombre},
	} {
		if err := s.guardarAjuste(r.Context(), org, ajuste.clave, ajuste.valor); err != nil {
			s.errorInterno(w, "guardar WhatsApp Zernio", err)
			return
		}
	}
	if err = s.guardarSecreto(r.Context(), org, "whatsapp_zernio_token", e.Token); err != nil {
		s.errorInterno(w, "cifrar token de Zernio", err)
		return
	}
	if err = s.guardarSecreto(r.Context(), org, "whatsapp_zernio_webhook_secret", secreto); err != nil {
		s.errorInterno(w, "cifrar secreto de Zernio", err)
		return
	}
	callback := strings.TrimRight(s.cfg.URLBase, "/") + "/api/webhooks/wa/" + s.tokenWebhook(org)
	payload := map[string]any{
		"name": "Nexora WhatsApp", "url": callback, "secret": secreto, "isActive": true,
		"accountIds": []string{e.CuentaID},
		"events":     []string{"message.received", "message.sent", "message.delivered", "message.read", "message.failed", "conversation.started"},
	}
	metodo := http.MethodPost
	if webhookID != "" {
		metodo = http.MethodPut
		payload["webhookId"] = webhookID
	}
	datos, err := s.ejecutarZernio(r.Context(), e.Token, metodo, "/webhooks/settings", payload)
	if err != nil {
		responderError(w, http.StatusBadGateway, "zernio_webhook_failed", "La cuenta es válida, pero Zernio no permitió configurar el webhook: "+err.Error(), nil)
		return
	}
	if webhookID == "" {
		var salida struct {
			Webhook struct {
				ID string `json:"_id"`
			} `json:"webhook"`
		}
		if json.Unmarshal(datos, &salida) != nil || salida.Webhook.ID == "" {
			responderError(w, http.StatusBadGateway, "zernio_invalid_response", "Zernio creó el webhook, pero no devolvió su identificador", nil)
			return
		}
		webhookID = salida.Webhook.ID
	}
	if err = s.guardarAjuste(r.Context(), org, "whatsapp_zernio_webhook_id", webhookID); err != nil {
		s.errorInterno(w, "guardar webhook de Zernio", err)
		return
	}
	if _, err = s.ejecutarZernio(r.Context(), e.Token, http.MethodPost, "/webhooks/test", map[string]string{"webhookId": webhookID}); err != nil {
		responderError(w, http.StatusBadGateway, "zernio_webhook_test_failed", "El webhook fue creado, pero Zernio no pudo entregar la prueba a Nexora: "+err.Error(), nil)
		return
	}
	if err = s.guardarAjuste(r.Context(), org, "whatsapp_source", "zernio"); err != nil {
		s.errorInterno(w, "activar WhatsApp por Zernio", err)
		return
	}
	estado, err := s.estadoWhatsApp(r.Context(), org)
	if err != nil {
		s.errorInterno(w, "leer WhatsApp Zernio", err)
		return
	}
	responderJSON(w, http.StatusOK, estado)
}

func (s *Servidor) listarCuentasWhatsAppZernio(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if validarRol(sesion, "owner", "admin") != nil {
		responderError(w, http.StatusForbidden, "forbidden", "Solo owner o admin puede configurar WhatsApp", nil)
		return
	}
	var entrada struct {
		Token string `json:"token"`
	}
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.Token = strings.TrimSpace(entrada.Token)
	if entrada.Token == "" {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Ingresa el token de Zernio", nil)
		return
	}
	datos, err := s.ejecutarZernio(r.Context(), entrada.Token, http.MethodGet, "/accounts?platform=whatsapp&excludeHidden=true", nil)
	if err != nil {
		responderError(w, http.StatusUnprocessableEntity, "zernio_connection_failed", err.Error(), nil)
		return
	}
	var salida struct {
		Accounts []struct {
			ID          string `json:"_id"`
			Platform    string `json:"platform"`
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
			IsActive    bool   `json:"isActive"`
		} `json:"accounts"`
	}
	if err = json.Unmarshal(datos, &salida); err != nil {
		responderError(w, http.StatusBadGateway, "zernio_invalid_response", "Zernio devolvió una lista de cuentas inválida", nil)
		return
	}
	items := make([]cuentaWhatsAppZernio, 0, len(salida.Accounts))
	for _, cuenta := range salida.Accounts {
		if !strings.EqualFold(cuenta.Platform, "whatsapp") || cuenta.ID == "" {
			continue
		}
		nombre := strings.TrimSpace(cuenta.DisplayName)
		if nombre == "" {
			nombre = strings.TrimSpace(cuenta.Username)
		}
		estado := "requiere reconexión"
		if cuenta.IsActive {
			estado = "conectada"
		}
		items = append(items, cuentaWhatsAppZernio{ID: cuenta.ID, Nombre: nombre, Usuario: cuenta.Username, Estado: estado})
	}
	responderJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Servidor) comprobarCuentaWhatsAppZernio(ctx context.Context, token, cuentaID string) (cuentaWhatsAppZernio, error) {
	datos, err := s.ejecutarZernio(ctx, token, http.MethodGet, "/accounts/"+url.PathEscape(cuentaID)+"/health", nil)
	if err != nil {
		return cuentaWhatsAppZernio{}, err
	}
	var salud struct {
		AccountID, Platform, Username, DisplayName, Status string
		Issues                                             []string `json:"issues"`
		PlatformConnection                                 struct {
			Status                   string `json:"status"`
			PhoneStatus              string `json:"phoneStatus"`
			InboundWebhookSubscribed *bool  `json:"inboundWebhookSubscribed"`
		} `json:"platformConnection"`
	}
	if err = json.Unmarshal(datos, &salud); err != nil {
		return cuentaWhatsAppZernio{}, errors.New("Zernio devolvió un estado de cuenta inválido")
	}
	if !strings.EqualFold(salud.Platform, "whatsapp") {
		return cuentaWhatsAppZernio{}, errors.New("la cuenta elegida no es de WhatsApp")
	}
	if salud.Status == "error" || salud.PlatformConnection.Status == "disconnected" {
		detalle := strings.Join(salud.Issues, "; ")
		if detalle == "" {
			detalle = "reconecta el número en Zernio"
		}
		return cuentaWhatsAppZernio{}, fmt.Errorf("la cuenta de WhatsApp no está operativa: %s", detalle)
	}
	if salud.PlatformConnection.InboundWebhookSubscribed != nil && !*salud.PlatformConnection.InboundWebhookSubscribed {
		return cuentaWhatsAppZernio{}, errors.New("Zernio no está recibiendo los mensajes entrantes de Meta; reconecta el número en Zernio")
	}
	if salud.PlatformConnection.PhoneStatus != "" && !strings.EqualFold(salud.PlatformConnection.PhoneStatus, "CONNECTED") {
		return cuentaWhatsAppZernio{}, fmt.Errorf("el número figura como %s en Meta; corrígelo en Zernio antes de conectarlo", salud.PlatformConnection.PhoneStatus)
	}
	nombre := strings.TrimSpace(salud.DisplayName)
	if nombre == "" {
		nombre = strings.TrimSpace(salud.Username)
	}
	return cuentaWhatsAppZernio{ID: cuentaID, Nombre: nombre, Usuario: salud.Username, Estado: salud.Status}, nil
}
