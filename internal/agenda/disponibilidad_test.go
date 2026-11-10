package agenda

import (
	"testing"
	"time"
)

func TestHuecosRespetaHorarioAvisoBufferYReservas(t *testing.T) {
	zona, _ := time.LoadLocation("America/Santiago")
	ahora := time.Date(2026, 11, 9, 10, 0, 0, 0, zona)
	cfg := Configuracion{Horario: HorarioSemanal{"lunes": {{Desde: "09:00", Hasta: "14:00"}}}, Zona: zona, Duracion: 30 * time.Minute, Buffer: 15 * time.Minute, AvisoMinimo: time.Hour, DiasAdelante: 1}
	reservas := []Reserva{{Inicio: time.Date(2026, 11, 9, 11, 30, 0, 0, zona).UTC(), Duracion: 30 * time.Minute}}
	huecos, err := Huecos(cfg, ahora, reservas, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(huecos) != 2 || huecos[0].Inicio.In(zona).Hour() != 12 || huecos[0].Inicio.In(zona).Minute() != 45 {
		t.Fatalf("huecos inesperados: %#v", huecos)
	}
}

func TestValidarHorarioRechazaTramosInvalidos(t *testing.T) {
	if ValidarHorario(HorarioSemanal{"feriado": {{Desde: "09:00", Hasta: "10:00"}}}) == nil {
		t.Fatal("acepto un dia desconocido")
	}
	if ValidarHorario(HorarioSemanal{"lunes": {{Desde: "18:00", Hasta: "09:00"}}}) == nil {
		t.Fatal("acepto un tramo invertido")
	}
}
