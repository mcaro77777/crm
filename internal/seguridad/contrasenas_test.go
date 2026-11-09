package seguridad

import "testing"

func TestHashYVerificacion(t *testing.T) {
	hash, err := HashContrasena("una-clave-segura-2026")
	if err != nil {
		t.Fatal(err)
	}
	if !VerificarContrasena("una-clave-segura-2026", hash) {
		t.Fatal("la clave correcta no fue aceptada")
	}
	if VerificarContrasena("clave-incorrecta", hash) {
		t.Fatal("la clave incorrecta fue aceptada")
	}
}

func TestRechazaContrasenaCorta(t *testing.T) {
	if _, err := HashContrasena("corta"); err == nil {
		t.Fatal("se esperaba rechazo")
	}
}

func TestHashTokenDependeDelSecreto(t *testing.T) {
	a := HashTokenConSecreto("token", "secreto-uno")
	b := HashTokenConSecreto("token", "secreto-dos")
	if a == b {
		t.Fatal("dos secretos distintos no deben producir el mismo hash")
	}
}
