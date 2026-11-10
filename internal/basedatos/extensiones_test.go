package basedatos

import (
	"os"
	"strings"
	"testing"
)

func TestExtensionLaboratorioConservaAislamiento(t *testing.T) {
	datos, err := os.ReadFile("extensiones.go")
	if err != nil {
		t.Fatal(err)
	}
	texto := strings.ToLower(string(datos))
	for _, fragmento := range []string{"create table if not exists lab_scenario", "create table if not exists lab_case_snapshot", "organization_id", "foreign key (organization_id)", "on delete cascade"} {
		if !strings.Contains(texto, fragmento) {
			t.Fatalf("la extensión de laboratorio no contiene %q", fragmento)
		}
	}
}
