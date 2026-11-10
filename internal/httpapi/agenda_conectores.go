package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crm-api/internal/seguridad"
)

var (
	zoomTokenURL   = "https://zoom.us/oauth/token"
	zoomAPIBase    = "https://api.zoom.us/v2"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleAPIBase  = "https://www.googleapis.com/calendar/v3"
)

type resultadoConectorAgenda struct {
	Referencia string
	Enlace     string
}

func clienteAgendaHTTP() *http.Client {
	return &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func tokenZoom(ctx context.Context, cliente *http.Client, accountID, clientID, secreto string) (string, error) {
	form := url.Values{"grant_type": {"account_credentials"}, "account_id": {accountID}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, zoomTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":"+secreto)))
	var salida struct {
		AccessToken string `json:"access_token"`
	}
	if err := ejecutarAgendaHTTP(cliente, req, &salida); err != nil {
		return "", fmt.Errorf("Zoom no acepto las credenciales: %w", err)
	}
	if salida.AccessToken == "" {
		return "", errors.New("Zoom no devolvio un token")
	}
	return salida.AccessToken, nil
}

func tokenGoogle(ctx context.Context, cliente *http.Client, clientID, secreto, refresh string) (string, error) {
	form := url.Values{"client_id": {clientID}, "client_secret": {secreto}, "refresh_token": {refresh}, "grant_type": {"refresh_token"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var salida struct {
		AccessToken string `json:"access_token"`
	}
	if err := ejecutarAgendaHTTP(cliente, req, &salida); err != nil {
		return "", fmt.Errorf("Google no acepto las credenciales: %w", err)
	}
	if salida.AccessToken == "" {
		return "", errors.New("Google no devolvio un token")
	}
	return salida.AccessToken, nil
}

func ejecutarAgendaHTTP(cliente *http.Client, req *http.Request, salida any) error {
	res, err := cliente.Do(req)
	if err != nil {
		return errors.New("el proveedor no respondio")
	}
	defer res.Body.Close()
	datos, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(datos) > 2<<20 {
		return errors.New("respuesta del proveedor invalida")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("estado HTTP %d", res.StatusCode)
	}
	if salida != nil && len(datos) > 0 && json.Unmarshal(datos, salida) != nil {
		return errors.New("respuesta JSON invalida")
	}
	return nil
}

func (s *Servidor) crearReunionAgenda(ctx context.Context, org, conector, titulo string, inicio time.Time, duracion int, prueba bool) (resultadoConectorAgenda, error) {
	if prueba {
		return resultadoConectorAgenda{}, nil
	}
	switch conector {
	case "enlace-fijo":
		var enlace sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT meeting_link FROM calendar_settings WHERE organization_id=?`, org).Scan(&enlace); err != nil {
			return resultadoConectorAgenda{}, err
		}
		if !enlace.Valid || strings.TrimSpace(enlace.String) == "" {
			return resultadoConectorAgenda{}, errors.New("configura el enlace fijo de la agenda")
		}
		return resultadoConectorAgenda{Enlace: enlace.String}, nil
	case "zoom":
		var account, client, cifrado, iv, tag string
		if err := s.db.QueryRowContext(ctx, `SELECT account_id,client_id,secret_cipher,secret_iv,secret_tag FROM zoom_credentials WHERE organization_id=? AND status='connected'`, org).Scan(&account, &client, &cifrado, &iv, &tag); err != nil {
			return resultadoConectorAgenda{}, errors.New("Zoom no esta conectado")
		}
		secreto, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
		if err != nil {
			return resultadoConectorAgenda{}, err
		}
		token, err := tokenZoom(ctx, clienteAgendaHTTP(), account, client, secreto)
		if err != nil {
			return resultadoConectorAgenda{}, err
		}
		payload := map[string]any{"topic": titulo, "type": 2, "start_time": inicio.UTC().Format(time.RFC3339), "duration": duracion, "timezone": "UTC", "settings": map[string]any{"waiting_room": true}}
		return crearZoom(ctx, token, payload)
	case "google":
		var client, cifSecret, ivSecret, tagSecret, cifRefresh, ivRefresh, tagRefresh, calendar string
		err := s.db.QueryRowContext(ctx, `SELECT client_id,client_secret_cipher,client_secret_iv,client_secret_tag,refresh_token_cipher,refresh_token_iv,refresh_token_tag,calendar_id FROM google_credentials WHERE organization_id=? AND status='connected'`, org).Scan(&client, &cifSecret, &ivSecret, &tagSecret, &cifRefresh, &ivRefresh, &tagRefresh, &calendar)
		if err != nil {
			return resultadoConectorAgenda{}, errors.New("Google Calendar no esta conectado")
		}
		secret, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifSecret, IV: ivSecret, Tag: tagSecret})
		if err != nil {
			return resultadoConectorAgenda{}, err
		}
		refresh, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifRefresh, IV: ivRefresh, Tag: tagRefresh})
		if err != nil {
			return resultadoConectorAgenda{}, err
		}
		token, err := tokenGoogle(ctx, clienteAgendaHTTP(), client, secret, refresh)
		if err != nil {
			return resultadoConectorAgenda{}, err
		}
		return crearGoogle(ctx, token, calendar, titulo, inicio, duracion)
	default:
		return resultadoConectorAgenda{}, errors.New("conector de agenda no soportado")
	}
}

func crearZoom(ctx context.Context, token string, payload any) (resultadoConectorAgenda, error) {
	cuerpo, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, zoomAPIBase+"/users/me/meetings", bytes.NewReader(cuerpo))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	var salida struct {
		ID      json.Number `json:"id"`
		JoinURL string      `json:"join_url"`
	}
	if err := ejecutarAgendaHTTP(clienteAgendaHTTP(), req, &salida); err != nil {
		return resultadoConectorAgenda{}, err
	}
	return resultadoConectorAgenda{Referencia: salida.ID.String(), Enlace: salida.JoinURL}, nil
}

func crearGoogle(ctx context.Context, token, calendar, titulo string, inicio time.Time, duracion int) (resultadoConectorAgenda, error) {
	requestID := fmt.Sprintf("nexora-%d", time.Now().UTC().UnixNano())
	payload := map[string]any{"summary": titulo, "start": map[string]string{"dateTime": inicio.UTC().Format(time.RFC3339), "timeZone": "UTC"}, "end": map[string]string{"dateTime": inicio.UTC().Add(time.Duration(duracion) * time.Minute).Format(time.RFC3339), "timeZone": "UTC"}, "conferenceData": map[string]any{"createRequest": map[string]any{"requestId": requestID, "conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"}}}}
	cuerpo, _ := json.Marshal(payload)
	destino := googleAPIBase + "/calendars/" + url.PathEscape(calendar) + "/events?conferenceDataVersion=1"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, destino, bytes.NewReader(cuerpo))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	var salida struct {
		ID             string `json:"id"`
		HangoutLink    string `json:"hangoutLink"`
		ConferenceData struct {
			EntryPoints []struct {
				EntryPointType string `json:"entryPointType"`
				URI            string `json:"uri"`
			} `json:"entryPoints"`
		} `json:"conferenceData"`
	}
	if err := ejecutarAgendaHTTP(clienteAgendaHTTP(), req, &salida); err != nil {
		return resultadoConectorAgenda{}, err
	}
	enlace := salida.HangoutLink
	if enlace == "" {
		for _, e := range salida.ConferenceData.EntryPoints {
			if e.EntryPointType == "video" {
				enlace = e.URI
				break
			}
		}
	}
	return resultadoConectorAgenda{Referencia: salida.ID, Enlace: enlace}, nil
}

func (s *Servidor) actualizarReunionAgenda(ctx context.Context, org, conector string, referencia *string, titulo string, inicio time.Time, duracion int) error {
	if conector == "enlace-fijo" || referencia == nil || strings.TrimSpace(*referencia) == "" {
		return nil
	}
	payload := map[string]any{"topic": titulo, "start_time": inicio.UTC().Format(time.RFC3339), "duration": duracion, "timezone": "UTC"}
	metodo, destino, token, err := s.datosEdicionConector(ctx, org, conector, *referencia)
	if err != nil {
		return err
	}
	if conector == "google" {
		payload = map[string]any{"summary": titulo, "start": map[string]string{"dateTime": inicio.UTC().Format(time.RFC3339), "timeZone": "UTC"}, "end": map[string]string{"dateTime": inicio.UTC().Add(time.Duration(duracion) * time.Minute).Format(time.RFC3339), "timeZone": "UTC"}}
	}
	cuerpo, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, metodo, destino, bytes.NewReader(cuerpo))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return ejecutarAgendaHTTP(clienteAgendaHTTP(), req, nil)
}

func (s *Servidor) cancelarReunionAgenda(ctx context.Context, org, conector string, referencia *string) error {
	if conector == "enlace-fijo" || referencia == nil || strings.TrimSpace(*referencia) == "" {
		return nil
	}
	_, destino, token, err := s.datosEdicionConector(ctx, org, conector, *referencia)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, destino, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return ejecutarAgendaHTTP(clienteAgendaHTTP(), req, nil)
}

func (s *Servidor) datosEdicionConector(ctx context.Context, org, conector, referencia string) (string, string, string, error) {
	switch conector {
	case "zoom":
		var account, client, cif, iv, tag string
		if err := s.db.QueryRowContext(ctx, `SELECT account_id,client_id,secret_cipher,secret_iv,secret_tag FROM zoom_credentials WHERE organization_id=? AND status='connected'`, org).Scan(&account, &client, &cif, &iv, &tag); err != nil {
			return "", "", "", err
		}
		secret, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cif, IV: iv, Tag: tag})
		if err != nil {
			return "", "", "", err
		}
		token, err := tokenZoom(ctx, clienteAgendaHTTP(), account, client, secret)
		return http.MethodPatch, zoomAPIBase + "/meetings/" + url.PathEscape(referencia), token, err
	case "google":
		var client, cif, iv, tag, rcif, riv, rtag, calendar string
		if err := s.db.QueryRowContext(ctx, `SELECT client_id,client_secret_cipher,client_secret_iv,client_secret_tag,refresh_token_cipher,refresh_token_iv,refresh_token_tag,calendar_id FROM google_credentials WHERE organization_id=? AND status='connected'`, org).Scan(&client, &cif, &iv, &tag, &rcif, &riv, &rtag, &calendar); err != nil {
			return "", "", "", err
		}
		secret, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cif, IV: iv, Tag: tag})
		if err != nil {
			return "", "", "", err
		}
		refresh, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: rcif, IV: riv, Tag: rtag})
		if err != nil {
			return "", "", "", err
		}
		token, err := tokenGoogle(ctx, clienteAgendaHTTP(), client, secret, refresh)
		return http.MethodPatch, googleAPIBase + "/calendars/" + url.PathEscape(calendar) + "/events/" + url.PathEscape(referencia), token, err
	default:
		return "", "", "", errors.New("conector no editable")
	}
}
