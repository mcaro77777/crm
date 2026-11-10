// Package agenda contiene el motor puro de disponibilidad de Nexora.
package agenda

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Tramo struct {
	Desde string `json:"desde"`
	Hasta string `json:"hasta"`
}

type HorarioSemanal map[string][]Tramo

type Reserva struct {
	Inicio   time.Time
	Duracion time.Duration
}

type Configuracion struct {
	Horario      HorarioSemanal
	Zona         *time.Location
	Duracion     time.Duration
	Buffer       time.Duration
	AvisoMinimo  time.Duration
	DiasAdelante int
}

type Hueco struct {
	Inicio time.Time `json:"inicio"`
	Fin    time.Time `json:"fin"`
	Label  string    `json:"label"`
}

var dias = map[time.Weekday]string{
	time.Monday: "lunes", time.Tuesday: "martes", time.Wednesday: "miercoles",
	time.Thursday: "jueves", time.Friday: "viernes", time.Saturday: "sabado", time.Sunday: "domingo",
}

func ValidarHorario(h HorarioSemanal) error {
	for dia, tramos := range h {
		valido := false
		for _, nombre := range dias {
			if dia == nombre {
				valido = true
				break
			}
		}
		if !valido {
			return fmt.Errorf("dia invalido: %s", dia)
		}
		for _, tramo := range tramos {
			desde, errDesde := reloj(tramo.Desde)
			hasta, errHasta := reloj(tramo.Hasta)
			if errDesde != nil || errHasta != nil || desde >= hasta {
				return fmt.Errorf("tramo invalido en %s", dia)
			}
		}
	}
	return nil
}

func Huecos(cfg Configuracion, ahora time.Time, reservas []Reserva, limite int) ([]Hueco, error) {
	if cfg.Zona == nil || cfg.Duracion < 5*time.Minute || cfg.Duracion > 8*time.Hour || cfg.Buffer < 0 || cfg.DiasAdelante < 1 {
		return nil, errors.New("configuracion de agenda invalida")
	}
	if err := ValidarHorario(cfg.Horario); err != nil {
		return nil, err
	}
	if limite < 1 || limite > 200 {
		limite = 30
	}
	ordenadas := append([]Reserva(nil), reservas...)
	sort.Slice(ordenadas, func(i, j int) bool { return ordenadas[i].Inicio.Before(ordenadas[j].Inicio) })
	minimo := ahora.UTC().Add(cfg.AvisoMinimo)
	local := ahora.In(cfg.Zona)
	inicioDia := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, cfg.Zona)
	resultado := make([]Hueco, 0, limite)
	for desplazamiento := 0; desplazamiento <= cfg.DiasAdelante && len(resultado) < limite; desplazamiento++ {
		dia := inicioDia.AddDate(0, 0, desplazamiento)
		for _, tramo := range cfg.Horario[dias[dia.Weekday()]] {
			desdeMin, _ := reloj(tramo.Desde)
			hastaMin, _ := reloj(tramo.Hasta)
			desde := time.Date(dia.Year(), dia.Month(), dia.Day(), desdeMin/60, desdeMin%60, 0, 0, cfg.Zona)
			hasta := time.Date(dia.Year(), dia.Month(), dia.Day(), hastaMin/60, hastaMin%60, 0, 0, cfg.Zona)
			for cursor := desde; !cursor.Add(cfg.Duracion).After(hasta) && len(resultado) < limite; cursor = cursor.Add(cfg.Duracion + cfg.Buffer) {
				inicioUTC := cursor.UTC()
				finUTC := cursor.Add(cfg.Duracion).UTC()
				if inicioUTC.Before(minimo) || seSolapa(inicioUTC, finUTC, cfg.Buffer, ordenadas) {
					continue
				}
				resultado = append(resultado, Hueco{Inicio: inicioUTC, Fin: finUTC, Label: etiqueta(cursor, cfg.Zona)})
			}
		}
	}
	return resultado, nil
}

func seSolapa(inicio, fin time.Time, buffer time.Duration, reservas []Reserva) bool {
	for _, reserva := range reservas {
		rInicio := reserva.Inicio.UTC().Add(-buffer)
		rFin := reserva.Inicio.UTC().Add(reserva.Duracion).Add(buffer)
		if inicio.Before(rFin) && fin.After(rInicio) {
			return true
		}
	}
	return false
}

func reloj(valor string) (int, error) {
	valor = strings.TrimSpace(valor)
	t, err := time.Parse("15:04", valor)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

func etiqueta(t time.Time, zona *time.Location) string {
	diasCortos := [...]string{"dom", "lun", "mar", "mie", "jue", "vie", "sab"}
	return fmt.Sprintf("%s %02d/%02d · %02d:%02d", diasCortos[t.In(zona).Weekday()], t.In(zona).Day(), t.In(zona).Month(), t.In(zona).Hour(), t.In(zona).Minute())
}
