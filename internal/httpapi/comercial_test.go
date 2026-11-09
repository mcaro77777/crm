package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestPeriodoAnalitica(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/analytics?desde=2026-01-01&hasta=2026-01-31", nil)
	desde, hasta, err := periodoAnalitica(r)
	if err != nil || desde.Format("2006-01-02") != "2026-01-01" || hasta.Format("2006-01-02") != "2026-02-01" {
		t.Fatalf("periodo incorrecto: %v %v %v", desde, hasta, err)
	}
	r = httptest.NewRequest("GET", "/api/analytics?desde=2024-01-01&hasta=2026-01-31", nil)
	if _, _, err := periodoAnalitica(r); err == nil {
		t.Fatal("aceptó más de 366 días")
	}
}

func TestEventoCAPIDeterminista(t *testing.T) {
	marca := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e := construirEventoCAPI("evt_1", "Purchase", marca, "clid_1", 15990, "CLP")
	if e["event_id"] != "evt_1" || e["event_time"] != marca.Unix() || e["action_source"] != "business_messaging" {
		t.Fatalf("evento inválido: %#v", e)
	}
	custom := e["custom_data"].(map[string]any)
	if custom["value"] != 159.9 || custom["currency"] != "CLP" {
		t.Fatalf("valor inválido: %#v", custom)
	}
}
