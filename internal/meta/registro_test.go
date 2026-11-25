package meta

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistroCoexistente(t *testing.T) {
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v27.0/oauth/access_token" && r.Header.Get("Authorization") != "Bearer token-prueba" {
			http.Error(w, "falta autorizacion", http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v27.0/oauth/access_token":
			if r.URL.Query().Get("client_secret") != "secreto-prueba" || r.URL.Query().Get("code") != "codigo+prueba" {
				http.Error(w, r.URL.RawQuery, http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"access_token":"token-prueba"}`)
		case "GET /v27.0/123":
			fmt.Fprint(w, `{"id":"123","display_phone_number":"+15550100","is_on_biz_app":true,"platform_type":"CLOUD_API"}`)
		case "GET /v27.0/456/phone_numbers":
			fmt.Fprint(w, `{"data":[{"id":"123"}]}`)
		case "GET /v27.0/789/phone_numbers":
			fmt.Fprint(w, `{"data":[{"id":"999"}]}`)
		case "POST /v27.0/456/subscribed_apps":
			fmt.Fprint(w, `{"success":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer servidor.Close()

	cliente := NuevoConBaseURL("", "v27.0", servidor.URL)
	if _, err := cliente.CanjearCodigoRegistro(t.Context(), "1", "secreto-privado", "codigo-privado"); err == nil ||
		strings.Contains(err.Error(), "secreto-privado") || strings.Contains(err.Error(), "codigo-privado") {
		t.Fatal("el canje debe fallar sin exponer valores de OAuth")
	}
	token, err := cliente.CanjearCodigoRegistro(t.Context(), "1", "secreto-prueba", "codigo+prueba")
	if err != nil {
		t.Fatal(err)
	}
	cliente.Token = token
	if _, err := cliente.ValidarTelefonoCoexistente(t.Context(), "789", "123"); err == nil {
		t.Fatal("se acepto un telefono ajeno a la cuenta de mensajes")
	}
	telefono, err := cliente.ValidarTelefonoCoexistente(t.Context(), "456", "123")
	if err != nil || telefono.ID != "123" {
		t.Fatalf("no se confirmo la coexistencia: %v", err)
	}
	if err := cliente.SuscribirCuentaWhatsApp(t.Context(), "456"); err != nil {
		t.Fatal(err)
	}
}
