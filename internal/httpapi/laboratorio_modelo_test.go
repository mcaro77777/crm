package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizarEscenarioLaboratorio(t *testing.T) {
	activo := true
	entrada, err := normalizarEscenario(entradaEscenarioLaboratorio{
		Nombre: "  Objecion de precio  ", Persona: "  Ana, clienta cautelosa ",
		Objetivo:  "  Resolver sin inventar descuentos ",
		Turnos:    []string{"  Hola, esta muy caro  ", "", "¿Tiene garantia?"},
		Criterios: []string{" No inventa precios ", "", " Explica garantia "}, Activo: &activo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if entrada.Nombre != "Objecion de precio" || entrada.Persona != "Ana, clienta cautelosa" {
		t.Fatalf("los campos no fueron normalizados: %#v", entrada)
	}
	if len(entrada.Turnos) != 2 || entrada.Turnos[0] != "Hola, esta muy caro" {
		t.Fatalf("turnos inesperados: %#v", entrada.Turnos)
	}
	if len(entrada.Criterios) != 2 {
		t.Fatalf("criterios inesperados: %#v", entrada.Criterios)
	}
}

func TestEscenarioLaboratorioExigeCasoCompleto(t *testing.T) {
	_, err := normalizarEscenario(entradaEscenarioLaboratorio{Nombre: "Caso", Persona: "Cliente", Objetivo: "Probar", Turnos: []string{"Hola"}})
	if err == nil || !strings.Contains(err.Error(), "criterios") {
		t.Fatalf("se esperaba error por criterios ausentes, se obtuvo %v", err)
	}
}

func TestEsquemaLaboratorioSiempreTieneOrganizacion(t *testing.T) {
	texto := strings.ToLower(esquemaLaboratorio + esquemaSnapshotLaboratorio)
	for _, fragmento := range []string{"organization_id", "foreign key (organization_id)", "on delete cascade"} {
		if !strings.Contains(texto, fragmento) {
			t.Fatalf("el esquema no contiene %q", fragmento)
		}
	}
}

func TestAgregarUnicosComparacion(t *testing.T) {
	obtenidos := agregarUnicos([]string{"No saludo"}, []string{" no saludo ", "No confirmo fecha"})
	if len(obtenidos) != 2 || obtenidos[1] != "No confirmo fecha" {
		t.Fatalf("hallazgos agregados incorrectamente: %#v", obtenidos)
	}
}
