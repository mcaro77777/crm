package httpapi

import (
	"net/http"
	"regexp"
	"strings"
)

const versionMetaRegistroInsertado = "v27.0"

var idMetaValido = regexp.MustCompile(`^[0-9]{1,32}$`)

type registroInsertadoMeta struct {
	AppID    string `json:"appId"`
	ConfigID string `json:"configId"`
	Version  string `json:"graphApiVersion"`
}

func (s *Servidor) configuracionRegistroInsertado() *registroInsertadoMeta {
	if !idMetaValido.MatchString(s.cfg.MetaEmbeddedAppID) ||
		!idMetaValido.MatchString(s.cfg.MetaEmbeddedConfigID) || s.cfg.MetaEmbeddedAppSecret == "" {
		return nil
	}
	return &registroInsertadoMeta{s.cfg.MetaEmbeddedAppID, s.cfg.MetaEmbeddedConfigID, versionMetaRegistroInsertado}
}

func (s *Servidor) conectarCoexistencia(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	if validarRol(sesion, "owner", "admin") != nil {
		responderError(w, http.StatusForbidden, "forbidden", "Solo owner o admin puede vincular WhatsApp", nil)
		return
	}
	config := s.configuracionRegistroInsertado()
	if config == nil {
		responderError(w, http.StatusServiceUnavailable, "embedded_signup_unavailable", "Configura el registro insertado de Meta en el servidor", nil)
		return
	}
	var entrada struct {
		Codigo     string `json:"code"`
		CuentaID   string `json:"messagingAccountId"`
		TelefonoID string `json:"phoneNumberId"`
	}
	if !decodificarJSON(w, r, &entrada) {
		return
	}
	entrada.Codigo = strings.TrimSpace(entrada.Codigo)
	if entrada.Codigo == "" || len(entrada.Codigo) > 4096 ||
		!idMetaValido.MatchString(entrada.CuentaID) || !idMetaValido.MatchString(entrada.TelefonoID) {
		responderError(w, 422, "validation_error", "Meta no entrego un codigo y una cuenta de WhatsApp validos", nil)
		return
	}
	ctx := r.Context()
	token, err := s.clienteMeta("", config.Version).CanjearCodigoRegistro(ctx, config.AppID, s.cfg.MetaEmbeddedAppSecret, entrada.Codigo)
	if err != nil {
		responderError(w, 422, "meta_oauth_failed", err.Error(), nil)
		return
	}
	cliente := s.clienteMeta(token, config.Version)
	telefono, err := cliente.ValidarTelefonoCoexistente(ctx, entrada.CuentaID, entrada.TelefonoID)
	if err != nil {
		responderError(w, 422, "meta_coexistence_unconfirmed", err.Error(), nil)
		return
	}
	if err := cliente.SuscribirCuentaWhatsApp(ctx, entrada.CuentaID); err != nil {
		responderError(w, 502, "meta_subscription_failed", err.Error(), nil)
		return
	}
	s.persistirConexionMeta(w, r, whatsappEntrada{
		Fuente: "meta", WABAID: entrada.CuentaID, TelefonoID: entrada.TelefonoID,
		Token: token, AppSecret: s.cfg.MetaEmbeddedAppSecret, Version: config.Version,
	}, telefono)
}
