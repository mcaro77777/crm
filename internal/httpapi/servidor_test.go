package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSPADevuelveFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>crm</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/contactos/uno", nil)
	res := httptest.NewRecorder()
	manejadorSPA(dir).ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "crm") {
		t.Fatalf("fallback inesperado: %d %s", res.Code, res.Body.String())
	}
}

func TestSPASinCompiladoEsControlado(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	manejadorSPA(t.TempDir()).ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("estado inesperado: %d", res.Code)
	}
}

func TestOrigenPermitido(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://crm.ejemplo.cl/api/test", nil)
	req.Header.Set("Origin", "https://crm.ejemplo.cl")
	if !origenPermitido(req, "https://crm.ejemplo.cl") {
		t.Fatal("el mismo origen debe permitirse")
	}
	req.Header.Set("Origin", "https://malicioso.example")
	if origenPermitido(req, "https://crm.ejemplo.cl") {
		t.Fatal("un origen distinto no debe permitirse")
	}
}

func TestLimitadorDeIntentos(t *testing.T) {
	l := nuevoLimitadorIntentos(2, time.Minute)
	ahora := time.Now()
	if !l.permitir("ip", ahora) || !l.permitir("ip", ahora) {
		t.Fatal("los primeros intentos deben permitirse")
	}
	if l.permitir("ip", ahora) {
		t.Fatal("el intento sobre el limite debe rechazarse")
	}
	if !l.permitir("ip", ahora.Add(time.Minute)) {
		t.Fatal("una ventana nueva debe permitir intentos")
	}
}
