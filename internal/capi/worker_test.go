package capi

import (
	"testing"
	"time"
)

func TestEsperaProgresiva(t *testing.T) {
	esperadas := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, e := range esperadas {
		if a := espera(i + 1); a != e {
			t.Fatalf("intento %d: %s", i+1, a)
		}
	}
}
