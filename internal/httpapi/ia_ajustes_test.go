package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbarProveedorIAValidaTokenRutaYModelo(t *testing.T) {
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("ruta inesperada: %s", r.URL.Path)
		}
		if recibido := r.Header.Get("Authorization"); recibido != "Bearer secreto-prueba" {
			t.Fatalf("Authorization inesperado: %q", recibido)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1-mini"},{"id":"otro-modelo"}]}`))
	}))
	defer servidor.Close()

	err := probarProveedorIA(context.Background(), servidor.URL+"/v1/", "secreto-prueba", "gpt-4.1-mini")
	if err != nil {
		t.Fatalf("se esperaba una conexión válida: %v", err)
	}
}

func TestProbarProveedorIAInformaModeloNoDisponible(t *testing.T) {
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"modelo-disponible"}]}`))
	}))
	defer servidor.Close()

	err := probarProveedorIA(context.Background(), servidor.URL, "secreto-prueba", "modelo-inexistente")
	if err == nil || !strings.Contains(err.Error(), `modelo "modelo-inexistente" no está disponible`) {
		t.Fatalf("error inesperado: %v", err)
	}
}

func TestProbarProveedorIAMuestraErrorDelProveedor(t *testing.T) {
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"API key incorrecta"}}`))
	}))
	defer servidor.Close()

	err := probarProveedorIA(context.Background(), servidor.URL, "secreto-invalido", "gpt-4.1-mini")
	if err == nil || !strings.Contains(err.Error(), "(401): API key incorrecta") {
		t.Fatalf("error inesperado: %v", err)
	}
}
