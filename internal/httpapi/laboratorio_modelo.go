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

const esquemaLaboratorio = `
CREATE TABLE IF NOT EXISTS lab_scenario (
  id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  organization_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  nombre VARCHAR(160) NOT NULL,
  persona VARCHAR(255) NOT NULL,
  objetivo TEXT NOT NULL,
  turnos JSON NOT NULL,
  criterios JSON NOT NULL,
  activo TINYINT NOT NULL DEFAULT 1,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY lab_scenario_org_idx (organization_id, activo, updated_at),
  CONSTRAINT fk_lab_scenario_org FOREIGN KEY (organization_id) REFERENCES organization(id) ON DELETE CASCADE,
  CONSTRAINT lab_scenario_activo_ck CHECK (activo IN (0,1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`

const esquemaSnapshotLaboratorio = `
CREATE TABLE IF NOT EXISTS lab_case_snapshot (
  case_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  organization_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  scenario_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  nombre VARCHAR(160) NOT NULL,
  objetivo TEXT NOT NULL,
  turnos JSON NOT NULL,
  criterios JSON NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (case_id),
  KEY lab_case_snapshot_org_idx (organization_id, scenario_id),
  CONSTRAINT fk_lab_case_snapshot_case FOREIGN KEY (case_id) REFERENCES agent_test_case(id) ON DELETE CASCADE,
  CONSTRAINT fk_lab_case_snapshot_org FOREIGN KEY (organization_id) REFERENCES organization(id) ON DELETE CASCADE,
  CONSTRAINT fk_lab_case_snapshot_scenario FOREIGN KEY (scenario_id) REFERENCES lab_scenario(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`

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

func (s *Servidor) asegurarEsquemaLaboratorio(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, esquemaLaboratorio); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, esquemaSnapshotLaboratorio)
	return err
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
