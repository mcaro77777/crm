package despacho

import (
	"testing"
	"time"
)

func TestEstadoSeguroNoFiltraElErrorTecnico(t *testing.T) {
	ahora := time.Now().UTC()
	casos := []struct {
		nombre, persistido, esperado string
		intentos                     int
	}{
		{"espera", "pendiente", "esperando", 0},
		{"reintento", "pendiente", "reintentando", 1},
		{"proceso", "en_vuelo", "procesando", 1},
		{"entregado", "entregado", "entregado", 1},
		{"agotado", "caducado_a_humano", "atencion_humana", 3},
		{"handoff", "descartado", "atencion_humana", 1},
		{"cancelado", "descartado", "cancelado", 1},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			errorInterno := "token secreto https://interno.example/cliente"
			if caso.nombre == "handoff" {
				errorInterno = "handoff: manual_reply"
			}
			estado := estadoSeguro(estadoPersistido{Estado: caso.persistido, Intentos: caso.intentos, NoAntes: ahora, ActualizadoEn: ahora, UltimoError: errorInterno})
			if estado.Estado != caso.esperado {
				t.Fatalf("estado=%s; esperado=%s", estado.Estado, caso.esperado)
			}
			if contiene(estado.Detalle, "secreto") || contiene(estado.Detalle, "interno.example") {
				t.Fatalf("se filtro el error tecnico: %s", estado.Detalle)
			}
		})
	}
}

func contiene(valor, parte string) bool {
	for i := 0; i+len(parte) <= len(valor); i++ {
		if valor[i:i+len(parte)] == parte {
			return true
		}
	}
	return false
}
