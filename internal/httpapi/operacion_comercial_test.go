package httpapi

import "testing"

func TestRolesEquipo(t *testing.T) {
	for _, rol := range []string{"owner", "admin", "agent"} {
		if !rolEquipoValido(rol) {
			t.Fatalf("rol válido rechazado: %s", rol)
		}
	}
	if rolEquipoValido("superadmin") || puedeAdministrarEquipo("agent") {
		t.Fatal("permisos demasiado amplios")
	}
}

func TestClavesFichaOrdenadas(t *testing.T) {
	r := clavesOrdenadas(map[string]any{"z": 1, "a": 2})
	if len(r) != 2 || r[0] != "a" || r[1] != "z" {
		t.Fatalf("orden: %v", r)
	}
}
