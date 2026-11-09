package seguridad

import (
	"bytes"
	"testing"
)

func TestSecretoCifradoPuedeRecuperarse(t *testing.T) {
	llave := bytes.Repeat([]byte{7}, 32)
	secreto, err := CifrarSecreto(llave, "token-super-secreto")
	if err != nil {
		t.Fatal(err)
	}
	if secreto.Cifrado == "token-super-secreto" {
		t.Fatal("el secreto quedo en claro")
	}
	valor, err := DescifrarSecreto(llave, secreto)
	if err != nil || valor != "token-super-secreto" {
		t.Fatalf("valor inesperado %q, error %v", valor, err)
	}
}
