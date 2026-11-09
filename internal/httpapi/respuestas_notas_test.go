package httpapi

import "testing"

func TestValidarRespuestaRapida(t *testing.T) {
	atajo, texto, ok := validarRespuestaRapida(" /Horario ", "  Atendemos de 9 a 18. ")
	if !ok || atajo != "horario" || texto != "Atendemos de 9 a 18." {
		t.Fatalf("normalización incorrecta: %q %q %v", atajo, texto, ok)
	}
	for _, invalido := range []string{"", "dos palabras", "con\ttab"} {
		if _, _, valido := validarRespuestaRapida(invalido, "texto"); valido {
			t.Fatalf("atajo inválido aceptado: %q", invalido)
		}
	}
}
