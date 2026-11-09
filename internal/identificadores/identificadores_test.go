package identificadores

import (
	"strings"
	"testing"
)

func TestNuevoGeneraIDsDistintosConPrefijo(t *testing.T) {
	a, err := Nuevo("ct")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Nuevo("ct")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("dos IDs aleatorios no deben coincidir")
	}
	if !strings.HasPrefix(a, "ct_") {
		t.Fatalf("prefijo inesperado: %s", a)
	}
}
