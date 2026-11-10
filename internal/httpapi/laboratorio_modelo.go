package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type escenarioLaboratorio struct {
	ID            string    `json:"id"`
	Nombre        string    `json:"nombre"`
	Persona       string    `json:"persona"`
	Objetivo      string    `json:"objetivo"`
	Turnos        []string  `json:"turnos"`
	Criterios     []string  `json:"criterios"`
	Activo        bool      `json:"activo"`
	CreadoEn      time.Time `json:"creadoEn"`
	ActualizadoEn time.Time `json:"actualizadoEn"`
}

type entradaEscenarioLaboratorio struct {
	Nombre    string   `json:"nombre"`
	Persona   string   `json:"persona"`
	Objetivo  string   `json:"objetivo"`
	Turnos    []string `json:"turnos"`
	Criterios []string `json:"criterios"`
	Activo    *bool    `json:"activo,omitempty"`
}

func normalizarEscenario(e entradaEscenarioLaboratorio) (entradaEscenarioLaboratorio, error) {
	e.Nombre = strings.TrimSpace(e.Nombre)
	e.Persona = strings.TrimSpace(e.Persona)
	e.Objetivo = strings.TrimSpace(e.Objetivo)
	e.Turnos = limpiarTextos(e.Turnos, 12, 4096)
	e.Criterios = limpiarTextos(e.Criterios, 20, 500)
	if e.Nombre == "" || e.Persona == "" || e.Objetivo == "" || len(e.Turnos) == 0 || len(e.Criterios) == 0 {
		return e, errors.New("nombre, persona, objetivo, turnos y criterios son obligatorios")
	}
	if len([]rune(e.Nombre)) > 160 || len([]rune(e.Persona)) > 255 || len([]rune(e.Objetivo)) > 4000 {
		return e, errors.New("el escenario supera el largo permitido")
	}
	return e, nil
}

func limpiarTextos(items []string, maxItems, maxRunas int) []string {
	if len(items) > maxItems {
		items = items[:maxItems]
	}
	salida := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" && len([]rune(item)) <= maxRunas {
			salida = append(salida, item)
		}
	}
	return salida
}

func escanearEscenario(scanner interface{ Scan(...any) error }) (escenarioLaboratorio, error) {
	var e escenarioLaboratorio
	var turnos, criterios []byte
	err := scanner.Scan(&e.ID, &e.Nombre, &e.Persona, &e.Objetivo, &turnos, &criterios, &e.Activo, &e.CreadoEn, &e.ActualizadoEn)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(turnos, &e.Turnos); err != nil {
		return e, fmt.Errorf("decodificar turnos: %w", err)
	}
	if err := json.Unmarshal(criterios, &e.Criterios); err != nil {
		return e, fmt.Errorf("decodificar criterios: %w", err)
	}
	return e, nil
}

func (s *Servidor) cargarEscenario(ctx context.Context, org, id string) (escenarioLaboratorio, error) {
	return escanearEscenario(s.db.QueryRowContext(ctx, `SELECT id,nombre,persona,objetivo,turnos,criterios,activo,created_at,updated_at FROM lab_scenario WHERE organization_id=? AND id=?`, org, id))
}

func marshalJSON(valor any) string {
	b, _ := json.Marshal(valor)
	return string(b)
}

func esDuplicadoMySQL(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "1062") || strings.Contains(strings.ToLower(err.Error()), "duplicate"))
}

func nuloTiempo(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}
