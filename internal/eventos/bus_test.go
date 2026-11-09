package eventos

import "testing"

func TestRecuperaEventosSinCruzarOrganizaciones(t *testing.T) {
	bus := NuevoBus()
	bus.Publicar("org_a", "uno", map[string]int{"n": 1})
	primero := bus.historial["org_a"][0].ID
	bus.Publicar("org_b", "ajeno", nil)
	bus.Publicar("org_a", "dos", map[string]int{"n": 2})

	canal, pendientes, perdido, cancelar := bus.SuscribirDesde("org_a", primero)
	defer cancelar()
	if canal == nil || perdido || len(pendientes) != 1 || pendientes[0].Tipo != "dos" {
		t.Fatalf("catch-up incorrecto: perdidos=%v pendientes=%+v", perdido, pendientes)
	}
}

func TestAvisaCuandoElHistorialExpiro(t *testing.T) {
	bus := NuevoBus()
	for i := 0; i < historialMaximo+5; i++ {
		bus.Publicar("org", "cambio", i)
	}
	_, pendientes, perdido, cancelar := bus.SuscribirDesde("org", 1)
	defer cancelar()
	if !perdido || len(pendientes) != historialMaximo {
		t.Fatalf("no detecto hueco: perdido=%v pendientes=%d", perdido, len(pendientes))
	}
}

func TestIDsSonEstrictamenteCrecientes(t *testing.T) {
	bus := NuevoBus()
	for i := 0; i < 20; i++ {
		bus.Publicar("org", "cambio", i)
	}
	historial := bus.historial["org"]
	for i := 1; i < len(historial); i++ {
		if historial[i].ID <= historial[i-1].ID {
			t.Fatalf("IDs no monotónicos: %d <= %d", historial[i].ID, historial[i-1].ID)
		}
	}
}
