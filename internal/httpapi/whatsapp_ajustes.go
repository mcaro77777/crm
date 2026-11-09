package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/identificadores"
	"crm-api/internal/meta"
	"crm-api/internal/seguridad"
)

type whatsappEntrada struct {
	WABAID     string `json:"wabaId"`
	TelefonoID string `json:"phoneNumberId"`
	Token      string `json:"token"`
	AppSecret  string `json:"appSecret"`
	Version    string `json:"graphApiVersion"`
}

type whatsappEstado struct {
	Conectado        bool   `json:"conectado"`
	WABAID           string `json:"wabaId,omitempty"`
	TelefonoID       string `json:"phoneNumberId,omitempty"`
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
	estado := whatsappEstado{Version: s.leerAjuste(ctx, organizacionID, "meta_graph_version", "v25.0")}
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
	var entrada whatsappEntrada
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.WABAID = strings.TrimSpace(entrada.WABAID)
	entrada.TelefonoID = strings.TrimSpace(entrada.TelefonoID)
	entrada.Token = strings.TrimSpace(entrada.Token)
	entrada.AppSecret = strings.TrimSpace(entrada.AppSecret)
	entrada.Version = strings.TrimSpace(entrada.Version)
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
	estado, _ := s.estadoWhatsApp(r.Context(), sesion.OrganizacionID)
	responderJSON(w, http.StatusOK, estado)
}

func (s *Servidor) probarWhatsApp(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
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
	_, err := s.db.ExecContext(r.Context(), "DELETE FROM meta_credentials WHERE organization_id=?", sesion.OrganizacionID)
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
	var cifrado, iv, tag string
	err := s.db.QueryRowContext(ctx, "SELECT waba_id,phone_number_id,token_cipher,token_iv,token_tag FROM meta_credentials WHERE organization_id=?", organizacionID).Scan(&c.WABAID, &c.TelefonoID, &cifrado, &iv, &tag)
	if err != nil {
		return c, err
	}
	c.Token, err = seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
	c.Version = s.leerAjuste(ctx, organizacionID, "meta_graph_version", "v25.0")
	return c, err
}
