package httpapi

import (
	"encoding/json"
	"testing"
	"time"
)

func TestConocimientoUsaContratoJSON(t *testing.T) {
	pregunta := "¿Cuál es el horario?"
	ahora := time.Date(2026, time.November, 9, 17, 0, 0, 0, time.UTC)
	cuerpo, err := json.Marshal(conocimientoDTO{
		ID:            "kb_1",
		Tipo:          "qa",
		Pregunta:      &pregunta,
		CreadoEn:      ahora,
		ActualizadoEn: ahora,
	})
	if err != nil {
		t.Fatal(err)
	}

	var salida map[string]any
	if err := json.Unmarshal(cuerpo, &salida); err != nil {
		t.Fatal(err)
	}
	for _, clave := range []string{"id", "tipo", "pregunta", "respuesta", "contenido", "creadoEn", "actualizadoEn"} {
		if _, existe := salida[clave]; !existe {
			t.Fatalf("falta la clave JSON %q en %s", clave, cuerpo)
		}
	}
	if _, existe := salida["ID"]; existe {
		t.Fatalf("el contrato no debe exponer nombres de campos Go: %s", cuerpo)
	}
}

func TestConocimientoValidoSegunFormato(t *testing.T) {
	casos := []struct {
		nombre string
		valor  conocimientoEntrada
		valido bool
	}{
		{"pregunta y respuesta", conocimientoEntrada{Tipo: "qa", Pregunta: "Pregunta", Respuesta: "Respuesta"}, true},
		{"pregunta sin respuesta", conocimientoEntrada{Tipo: "qa", Pregunta: "Pregunta"}, false},
		{"bloque informativo", conocimientoEntrada{Tipo: "block", Contenido: "Contenido"}, true},
		{"bloque vacío", conocimientoEntrada{Tipo: "block", Contenido: "   "}, false},
		{"formato desconocido", conocimientoEntrada{Tipo: "otro", Contenido: "Contenido"}, false},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			if obtenido := conocimientoValido(caso.valor); obtenido != caso.valido {
				t.Fatalf("conocimientoValido() = %v; se esperaba %v", obtenido, caso.valido)
			}
		})
	}
}
