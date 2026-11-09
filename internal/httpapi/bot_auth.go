package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net"
	"net/http"
	"strings"
	"time"

	"crm-api/internal/seguridad"
)

const claveBotOrg claveContexto = "bot_organization_id"

func organizacionBot(ctx context.Context) string {
	valor, _ := ctx.Value(claveBotOrg).(string)
	return valor
}

func (s *Servidor) requerirBot(siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ipRemota(r)
		clave := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if clave == "" {
			autorizacion := strings.TrimSpace(r.Header.Get("Authorization"))
			if len(autorizacion) > 7 && strings.EqualFold(autorizacion[:7], "Bearer ") {
				clave = strings.TrimSpace(autorizacion[7:])
			}
		}
		organizacionID, err := s.organizacionPorClaveBot(r.Context(), clave)
		if err != nil || organizacionID == "" {
			if !s.fallosBot.permitir(ip, time.Now()) {
				w.Header().Set("Retry-After", "60")
				responderError(w, http.StatusTooManyRequests, "rate_limited", "Demasiados intentos de autenticacion", nil)
				return
			}
			responderError(w, http.StatusUnauthorized, "unauthorized", "API key invalida", nil)
			return
		}
		if !s.limiteBot.permitir(organizacionID, time.Now()) {
			w.Header().Set("Retry-After", "60")
			responderError(w, http.StatusTooManyRequests, "rate_limited", "Limite de solicitudes alcanzado", nil)
			return
		}
		ctx := context.WithValue(r.Context(), claveBotOrg, organizacionID)
		siguiente.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Servidor) organizacionPorClaveBot(ctx context.Context, candidata string) (string, error) {
	if len(candidata) < 16 {
		return "", nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT organization_id,secret_cipher,secret_iv,secret_tag FROM app_setting WHERE clave='brain_api_key' AND secret_cipher IS NOT NULL`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	resuelta := ""
	huellaCandidata := sha256.Sum256([]byte(candidata))
	for rows.Next() {
		var org, cifrado, iv, tag string
		if err := rows.Scan(&org, &cifrado, &iv, &tag); err != nil {
			return "", err
		}
		secreto, err := seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
		huellaGuardada := sha256.Sum256([]byte(secreto))
		if err == nil && hmac.Equal(huellaGuardada[:], huellaCandidata[:]) {
			resuelta = org
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return resuelta, nil
}

func ipRemota(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
