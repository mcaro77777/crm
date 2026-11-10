package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/meta"
	"crm-api/internal/seguridad"
)

type whatsappEntrada struct {
	Fuente        string `json:"fuente"`
	WABAID        string `json:"wabaId"`
	TelefonoID    string `json:"phoneNumberId"`
	CuentaID      string `json:"cuentaId"`
	Nombre        string `json:"nombre"`
	Token         string `json:"token"`
	AppSecret     string `json:"appSecret"`
	WebhookSecret string `json:"webhookSecret"`
	Version       string `json:"graphApiVersion"`
}

type whatsappEstado struct {
	Conectado        bool   `json:"conectado"`
	Fuente           string `json:"fuente"`
	WABAID           string `json:"wabaId,omitempty"`
	TelefonoID       string `json:"phoneNumberId,omitempty"`
	CuentaID         string `json:"cuentaId,omitempty"`
	Telefono         string `json:"telefono,omitempty"`
	NombreVerificado string `json:"nombreVerificado,omitempty"`
	TokenUltimos4    string `json:"tokenUltimos4,omitempty"`
	Version          string `json:"graphApiVersion"`
	WebhookURL       string `json:"webhookUrl,omitempty"`
	VerifyToken      string `json:"verifyToken,omitempty"`
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
	entrada.WebhookSecret = strings.TrimSpace(entrada.WebhookSecret)
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
		if err = s.probarCredencialSocial(r.Context(), cred); err != nil {
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
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM app_setting WHERE organization_id=? AND clave IN ('whatsapp_source','whatsapp_zernio_account_id','whatsapp_zernio_name','whatsapp_zernio_token','whatsapp_zernio_webhook_secret')`, sesion.OrganizacionID)
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
	if e.CuentaID == "" || e.Token == "" || e.WebhookSecret == "" {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Account ID, token y secreto de webhook de Zernio son obligatorios", nil)
		return
	}
	cred := credencialCanal{Canal: "whatsapp", Fuente: "zernio", CuentaID: e.CuentaID, Nombre: e.Nombre, Token: e.Token, WebhookSecret: e.WebhookSecret}
	if err := s.probarCredencialSocial(r.Context(), cred); err != nil {
		responderError(w, http.StatusUnprocessableEntity, "zernio_connection_failed", err.Error(), nil)
		return
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
	if err := s.guardarSecreto(r.Context(), org, "whatsapp_zernio_token", e.Token); err != nil {
		s.errorInterno(w, "cifrar token de Zernio", err)
		return
	}
	if err := s.guardarSecreto(r.Context(), org, "whatsapp_zernio_webhook_secret", e.WebhookSecret); err != nil {
		s.errorInterno(w, "cifrar secreto de Zernio", err)
		return
	}
	if err := s.guardarAjuste(r.Context(), org, "whatsapp_source", "zernio"); err != nil {
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
