package webhooks

import (
	"testing"
	"time"
)

func TestEsperaReintentoProgresiva(t *testing.T) {
	esperadas := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, esperada := range esperadas {
		if actual := EsperaReintento(i + 1); actual != esperada {
			t.Fatalf("intento %d: %s; esperado %s", i+1, actual, esperada)
		}
	}
}
