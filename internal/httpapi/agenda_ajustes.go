package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crm-api/internal/agenda"
	"crm-api/internal/identificadores"
	"crm-api/internal/seguridad"
)

type ajustesAgendaDTO struct {
	Configurada      bool                  `json:"configurada"`
	Horario          agenda.HorarioSemanal `json:"horario"`
	DuracionMinutos  int                   `json:"duracionMinutos"`
	BufferMinutos    int                   `json:"bufferMinutos"`
	AvisoMinimoHoras int                   `json:"avisoMinimoHoras"`
	DiasAdelante     int                   `json:"diasAdelante"`
	ZonaHoraria      string                `json:"zonaHoraria"`
	Conector         string                `json:"conector"`
	EnlaceFijo       string                `json:"enlaceFijo"`
	GoogleConectado  bool                  `json:"googleConectado"`
	GoogleCalendarID string                `json:"googleCalendarId,omitempty"`
	ZoomConectado    bool                  `json:"zoomConectado"`
}

type entradaAjustesAgenda struct {
	Horario          agenda.HorarioSemanal `json:"horario"`
	DuracionMinutos  int                   `json:"duracionMinutos"`
	BufferMinutos    int                   `json:"bufferMinutos"`
	AvisoMinimoHoras int                   `json:"avisoMinimoHoras"`
	DiasAdelante     int                   `json:"diasAdelante"`
	ZonaHoraria      string                `json:"zonaHoraria"`
	Conector         string                `json:"conector"`
	EnlaceFijo       string                `json:"enlaceFijo"`
}

func horarioDefecto() agenda.HorarioSemanal {
	return agenda.HorarioSemanal{"lunes": {{Desde: "09:00", Hasta: "18:00"}}, "martes": {{Desde: "09:00", Hasta: "18:00"}}, "miercoles": {{Desde: "09:00", Hasta: "18:00"}}, "jueves": {{Desde: "09:00", Hasta: "18:00"}}, "viernes": {{Desde: "09:00", Hasta: "18:00"}}}
}

func (s *Servidor) registrarRutasAgenda(mux *http.ServeMux) {
	mux.Handle("GET /api/settings/calendar", s.requerirSesion(http.HandlerFunc(s.obtenerAjustesAgenda)))
	mux.Handle("PUT /api/settings/calendar", s.requerirSesion(http.HandlerFunc(s.guardarAjustesAgenda)))
	mux.Handle("PUT /api/settings/calendar/google", s.requerirSesion(http.HandlerFunc(s.guardarGoogleAgenda)))
	mux.Handle("POST /api/settings/calendar/google/test", s.requerirSesion(http.HandlerFunc(s.probarGoogleAgenda)))
	mux.Handle("DELETE /api/settings/calendar/google", s.requerirSesion(http.HandlerFunc(s.eliminarGoogleAgenda)))
	mux.Handle("PUT /api/settings/calendar/zoom", s.requerirSesion(http.HandlerFunc(s.guardarZoomAgenda)))
	mux.Handle("POST /api/settings/calendar/zoom/test", s.requerirSesion(http.HandlerFunc(s.probarZoomAgenda)))
	mux.Handle("DELETE /api/settings/calendar/zoom", s.requerirSesion(http.HandlerFunc(s.eliminarZoomAgenda)))
	mux.Handle("GET /api/bookings", s.requerirSesion(http.HandlerFunc(s.listarCitas)))
	mux.Handle("POST /api/bookings", s.requerirSesion(http.HandlerFunc(s.crearCitaManual)))
	mux.Handle("PATCH /api/bookings/{id}", s.requerirSesion(http.HandlerFunc(s.actualizarCita)))
	mux.Handle("GET /api/bot/availability", s.requerirBot(http.HandlerFunc(s.disponibilidadBot)))
	mux.Handle("POST /api/bot/bookings", s.requerirBot(http.HandlerFunc(s.crearCitaBot)))
	mux.Handle("PATCH /api/bot/bookings", s.requerirBot(http.HandlerFunc(s.reprogramarCitaBot)))
}

func (s *Servidor) leerAjustesAgenda(ctx context.Context, org string) (ajustesAgendaDTO, error) {
	d := ajustesAgendaDTO{Horario: horarioDefecto(), DuracionMinutos: 30, AvisoMinimoHoras: 2, DiasAdelante: 14, ZonaHoraria: "America/Santiago", Conector: "enlace-fijo"}
	var raw []byte
	var enlace sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT weekly_hours,slot_minutes,buffer_minutes,min_notice_hours,max_days_ahead,timezone,connector,meeting_link FROM calendar_settings WHERE organization_id=?`, org).Scan(&raw, &d.DuracionMinutos, &d.BufferMinutos, &d.AvisoMinimoHoras, &d.DiasAdelante, &d.ZonaHoraria, &d.Conector, &enlace)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	if err == nil {
		d.Configurada = true
		_ = json.Unmarshal(raw, &d.Horario)
		if enlace.Valid {
			d.EnlaceFijo = enlace.String
		}
	}
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM google_credentials WHERE organization_id=? AND status='connected'),COALESCE((SELECT calendar_id FROM google_credentials WHERE organization_id=?),'')`, org, org).Scan(&d.GoogleConectado, &d.GoogleCalendarID)
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM zoom_credentials WHERE organization_id=? AND status='connected')`, org).Scan(&d.ZoomConectado)
	return d, nil
}

func (s *Servidor) obtenerAjustesAgenda(w http.ResponseWriter, r *http.Request) {
	d, err := s.leerAjustesAgenda(r.Context(), sesionDe(r.Context()).OrganizacionID)
	if err != nil {
		s.errorInterno(w, "leer agenda", err)
		return
	}
	responderJSON(w, http.StatusOK, d)
}

func validarEntradaAgenda(e entradaAjustesAgenda) error {
	if err := agenda.ValidarHorario(e.Horario); err != nil {
		return err
	}
	if e.DuracionMinutos < 5 || e.DuracionMinutos > 480 || e.BufferMinutos < 0 || e.BufferMinutos > 240 || e.AvisoMinimoHoras < 0 || e.AvisoMinimoHoras > 720 || e.DiasAdelante < 1 || e.DiasAdelante > 365 {
		return errors.New("duracion, buffer, aviso o rango fuera de limites")
	}
	if _, err := time.LoadLocation(strings.TrimSpace(e.ZonaHoraria)); err != nil {
		return errors.New("zona horaria invalida")
	}
	if e.Conector != "enlace-fijo" && e.Conector != "google" && e.Conector != "zoom" {
		return errors.New("conector invalido")
	}
	if e.Conector == "enlace-fijo" {
		u, err := url.ParseRequestURI(strings.TrimSpace(e.EnlaceFijo))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("el enlace fijo debe ser una URL HTTP o HTTPS")
		}
	}
	return nil
}

func (s *Servidor) guardarAjustesAgenda(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	if validarRol(ses, "owner", "admin") != nil {
		responderError(w, http.StatusForbidden, "forbidden", "Solo owner o admin puede configurar la agenda", nil)
		return
	}
	var e entradaAjustesAgenda
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.ZonaHoraria = strings.TrimSpace(e.ZonaHoraria)
	e.EnlaceFijo = strings.TrimSpace(e.EnlaceFijo)
	if err := validarEntradaAgenda(e); err != nil {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", err.Error(), nil)
		return
	}
	raw, _ := json.Marshal(e.Horario)
	ahora := time.Now().UTC()
	id, _ := identificadores.Nuevo("cal")
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO calendar_settings(id,organization_id,weekly_hours,slot_minutes,buffer_minutes,min_notice_hours,max_days_ahead,timezone,connector,meeting_link,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE weekly_hours=VALUES(weekly_hours),slot_minutes=VALUES(slot_minutes),buffer_minutes=VALUES(buffer_minutes),min_notice_hours=VALUES(min_notice_hours),max_days_ahead=VALUES(max_days_ahead),timezone=VALUES(timezone),connector=VALUES(connector),meeting_link=VALUES(meeting_link),updated_at=VALUES(updated_at)`, id, ses.OrganizacionID, string(raw), e.DuracionMinutos, e.BufferMinutos, e.AvisoMinimoHoras, e.DiasAdelante, e.ZonaHoraria, e.Conector, nuloVacio(e.EnlaceFijo), ahora, ahora)
	if err != nil {
		s.errorInterno(w, "guardar agenda", err)
		return
	}
	s.obtenerAjustesAgenda(w, r)
}

type entradaGoogleAgenda struct {
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
	CalendarID   string `json:"calendarID"`
}
type entradaZoomAgenda struct {
	AccountID    string `json:"accountID"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
}

func (s *Servidor) guardarGoogleAgenda(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	if validarRol(ses, "owner", "admin") != nil {
		responderError(w, 403, "forbidden", "No autorizado", nil)
		return
	}
	var e entradaGoogleAgenda
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.ClientID = strings.TrimSpace(e.ClientID)
	e.ClientSecret = strings.TrimSpace(e.ClientSecret)
	e.RefreshToken = strings.TrimSpace(e.RefreshToken)
	e.CalendarID = strings.TrimSpace(e.CalendarID)
	if e.CalendarID == "" {
		e.CalendarID = "primary"
	}
	if e.ClientID == "" || e.ClientSecret == "" || e.RefreshToken == "" {
		responderError(w, 422, "validation_error", "Client ID, Client Secret y Refresh Token son obligatorios", nil)
		return
	}
	if _, err := tokenGoogle(r.Context(), clienteAgendaHTTP(), e.ClientID, e.ClientSecret, e.RefreshToken); err != nil {
		responderError(w, 422, "google_connection_failed", err.Error(), nil)
		return
	}
	sec, _ := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, e.ClientSecret)
	ref, _ := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, e.RefreshToken)
	id, _ := identificadores.Nuevo("ggc")
	ahora := time.Now().UTC()
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO google_credentials(id,organization_id,client_id,client_secret_cipher,client_secret_iv,client_secret_tag,refresh_token_cipher,refresh_token_iv,refresh_token_tag,calendar_id,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,'connected',?,?) ON DUPLICATE KEY UPDATE client_id=VALUES(client_id),client_secret_cipher=VALUES(client_secret_cipher),client_secret_iv=VALUES(client_secret_iv),client_secret_tag=VALUES(client_secret_tag),refresh_token_cipher=VALUES(refresh_token_cipher),refresh_token_iv=VALUES(refresh_token_iv),refresh_token_tag=VALUES(refresh_token_tag),calendar_id=VALUES(calendar_id),status='connected',updated_at=VALUES(updated_at)`, id, ses.OrganizacionID, e.ClientID, sec.Cifrado, sec.IV, sec.Tag, ref.Cifrado, ref.IV, ref.Tag, e.CalendarID, ahora, ahora)
	if err != nil {
		s.errorInterno(w, "guardar Google Calendar", err)
		return
	}
	s.obtenerAjustesAgenda(w, r)
}

func (s *Servidor) guardarZoomAgenda(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	if validarRol(ses, "owner", "admin") != nil {
		responderError(w, 403, "forbidden", "No autorizado", nil)
		return
	}
	var e entradaZoomAgenda
	if !decodificarJSON(w, r, &e) {
		return
	}
	e.AccountID = strings.TrimSpace(e.AccountID)
	e.ClientID = strings.TrimSpace(e.ClientID)
	e.ClientSecret = strings.TrimSpace(e.ClientSecret)
	if e.AccountID == "" || e.ClientID == "" || e.ClientSecret == "" {
		responderError(w, 422, "validation_error", "Account ID, Client ID y Client Secret son obligatorios", nil)
		return
	}
	if _, err := tokenZoom(r.Context(), clienteAgendaHTTP(), e.AccountID, e.ClientID, e.ClientSecret); err != nil {
		responderError(w, 422, "zoom_connection_failed", err.Error(), nil)
		return
	}
	sec, _ := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, e.ClientSecret)
	id, _ := identificadores.Nuevo("zgc")
	ahora := time.Now().UTC()
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO zoom_credentials(id,organization_id,account_id,client_id,secret_cipher,secret_iv,secret_tag,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'connected',?,?) ON DUPLICATE KEY UPDATE account_id=VALUES(account_id),client_id=VALUES(client_id),secret_cipher=VALUES(secret_cipher),secret_iv=VALUES(secret_iv),secret_tag=VALUES(secret_tag),status='connected',updated_at=VALUES(updated_at)`, id, ses.OrganizacionID, e.AccountID, e.ClientID, sec.Cifrado, sec.IV, sec.Tag, ahora, ahora)
	if err != nil {
		s.errorInterno(w, "guardar Zoom", err)
		return
	}
	s.obtenerAjustesAgenda(w, r)
}

func (s *Servidor) probarGoogleAgenda(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	var client, cif, iv, tag, rcif, riv, rtag string
	err := s.db.QueryRowContext(r.Context(), `SELECT client_id,client_secret_cipher,client_secret_iv,client_secret_tag,refresh_token_cipher,refresh_token_iv,refresh_token_tag FROM google_credentials WHERE organization_id=?`, ses.OrganizacionID).Scan(&client, &cif, &iv, &tag, &rcif, &riv, &rtag)
	if err != nil {
		responderError(w, 409, "google_not_configured", "Google Calendar no esta configurado", nil)
		return
	}
	secret, _ := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cif, IV: iv, Tag: tag})
	refresh, _ := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: rcif, IV: riv, Tag: rtag})
	_, err = tokenGoogle(r.Context(), clienteAgendaHTTP(), client, secret, refresh)
	if err != nil {
		responderError(w, 502, "google_connection_failed", err.Error(), nil)
		return
	}
	responderJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Servidor) probarZoomAgenda(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	var account, client, cif, iv, tag string
	err := s.db.QueryRowContext(r.Context(), `SELECT account_id,client_id,secret_cipher,secret_iv,secret_tag FROM zoom_credentials WHERE organization_id=?`, ses.OrganizacionID).Scan(&account, &client, &cif, &iv, &tag)
	if err != nil {
		responderError(w, 409, "zoom_not_configured", "Zoom no esta configurado", nil)
		return
	}
	secret, _ := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cif, IV: iv, Tag: tag})
	_, err = tokenZoom(r.Context(), clienteAgendaHTTP(), account, client, secret)
	if err != nil {
		responderError(w, 502, "zoom_connection_failed", err.Error(), nil)
		return
	}
	responderJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Servidor) eliminarGoogleAgenda(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	if validarRol(ses, "owner", "admin") != nil {
		responderError(w, 403, "forbidden", "No autorizado", nil)
		return
	}
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM google_credentials WHERE organization_id=?`, ses.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "eliminar Google", err)
		return
	}
	w.WriteHeader(204)
}
func (s *Servidor) eliminarZoomAgenda(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	if validarRol(ses, "owner", "admin") != nil {
		responderError(w, 403, "forbidden", "No autorizado", nil)
		return
	}
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM zoom_credentials WHERE organization_id=?`, ses.OrganizacionID)
	if err != nil {
		s.errorInterno(w, "eliminar Zoom", err)
		return
	}
	w.WriteHeader(204)
}
