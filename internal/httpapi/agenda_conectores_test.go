package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConectoresAgendaInterpretanZoomYGoogle(t *testing.T) {
	originalZoomToken, originalZoomAPI := zoomTokenURL, zoomAPIBase
	originalGoogleToken, originalGoogleAPI := googleTokenURL, googleAPIBase
	defer func() {
		zoomTokenURL, zoomAPIBase = originalZoomToken, originalZoomAPI
		googleTokenURL, googleAPIBase = originalGoogleToken, originalGoogleAPI
	}()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zoom/token":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
				t.Error("Zoom no recibio autenticacion Basic")
			}
			_, _ = w.Write([]byte(`{"access_token":"zoom-token"}`))
		case r.URL.Path == "/zoom/users/me/meetings":
			_, _ = w.Write([]byte(`{"id":12345,"join_url":"https://zoom.example/j/12345"}`))
		case r.URL.Path == "/google/token":
			_, _ = w.Write([]byte(`{"access_token":"google-token"}`))
		case strings.HasPrefix(r.URL.Path, "/google/calendars/"):
			var entrada map[string]any
			_ = json.NewDecoder(r.Body).Decode(&entrada)
			if entrada["conferenceData"] == nil {
				t.Error("Google no recibio la solicitud de Meet")
			}
			_, _ = w.Write([]byte(`{"id":"evt-1","conferenceData":{"entryPoints":[{"entryPointType":"video","uri":"https://meet.google.com/abc-defg-hij"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer servidor.Close()
	zoomTokenURL, zoomAPIBase = servidor.URL+"/zoom/token", servidor.URL+"/zoom"
	googleTokenURL, googleAPIBase = servidor.URL+"/google/token", servidor.URL+"/google"

	ctx := context.Background()
	zoomToken, err := tokenZoom(ctx, servidor.Client(), "cuenta", "cliente", "secreto")
	if err != nil || zoomToken != "zoom-token" {
		t.Fatalf("token Zoom: %q %v", zoomToken, err)
	}
	zoom, err := crearZoom(ctx, zoomToken, map[string]any{"topic": "Prueba"})
	if err != nil || zoom.Referencia != "12345" || zoom.Enlace == "" {
		t.Fatalf("reunion Zoom: %#v %v", zoom, err)
	}
	googleToken, err := tokenGoogle(ctx, servidor.Client(), "cliente", "secreto", "refresh")
	if err != nil || googleToken != "google-token" {
		t.Fatalf("token Google: %q %v", googleToken, err)
	}
	google, err := crearGoogle(ctx, googleToken, "primary", "Prueba", time.Now().UTC(), 30)
	if err != nil || google.Referencia != "evt-1" || !strings.Contains(google.Enlace, "meet.google.com") {
		t.Fatalf("evento Google: %#v %v", google, err)
	}
}
