package canales

import (
	"testing"
	"time"
)

func TestCapacidadesEstanDeclaradasPorCanal(t *testing.T) {
	casos := map[string]time.Duration{"whatsapp": 24 * time.Hour, "instagram": 24 * time.Hour, "messenger": 24 * time.Hour, "tiktok": 48 * time.Hour}
	for canal, ventana := range casos {
		capacidad, ok := Obtener(canal)
		if !ok || capacidad.Ventana != ventana || capacidad.MaxCaracteres < 1 || capacidad.MaxMensajes < 1 {
			t.Fatalf("capacidad invalida para %s: %#v", canal, capacidad)
		}
	}
	if _, ok := Obtener("desconocido"); ok {
		t.Fatal("un canal desconocido no debe tener capacidades")
	}
}
