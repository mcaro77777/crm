package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"crm-api/internal/lab"
)

type resultadoCasoComparacion struct {
	EscenarioID string
	Persona     string
	Resultado   lab.ResultadoJuez
}

func (s *Servidor) compararCorridasLaboratorio(w http.ResponseWriter, r *http.Request) {
	sesion := sesionDe(r.Context())
	anteriorID := strings.TrimSpace(r.URL.Query().Get("anterior"))
	actualID := strings.TrimSpace(r.URL.Query().Get("actual"))
	if anteriorID == "" || actualID == "" || anteriorID == actualID {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "Indica dos corridas distintas: anterior y actual", nil)
		return
	}
	anteriorScore, anterior, err := s.resultadosParaComparar(r.Context(), sesion.OrganizacionID, anteriorID)
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, http.StatusNotFound, "not_found", "Corrida anterior no encontrada o incompleta", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer corrida anterior", err)
		return
	}
	actualScore, actual, err := s.resultadosParaComparar(r.Context(), sesion.OrganizacionID, actualID)
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, http.StatusNotFound, "not_found", "Corrida actual no encontrada o incompleta", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer corrida actual", err)
		return
	}

	type comparacionCaso struct {
		EscenarioID string          `json:"escenarioId"`
		Persona     string          `json:"persona"`
		Comparacion lab.Comparacion `json:"comparacion"`
	}
	casos := make([]comparacionCaso, 0)
	resueltos := make([]string, 0)
	nuevos := make([]string, 0)
	for escenarioID, nuevo := range actual {
		viejo, existe := anterior[escenarioID]
		if !existe {
			continue
		}
		comparacion := lab.CompararResultados(viejo.Resultado, nuevo.Resultado)
		casos = append(casos, comparacionCaso{EscenarioID: escenarioID, Persona: nuevo.Persona, Comparacion: comparacion})
		resueltos = agregarUnicos(resueltos, comparacion.ProblemasResueltos)
		nuevos = agregarUnicos(nuevos, comparacion.ProblemasNuevos)
	}
	sort.Slice(casos, func(i, j int) bool { return casos[i].EscenarioID < casos[j].EscenarioID })
	tendencia := "igual"
	if actualScore > anteriorScore {
		tendencia = "mejora"
	}
	if actualScore < anteriorScore {
		tendencia = "retroceso"
	}
	responderJSON(w, http.StatusOK, map[string]any{
		"anterior": anteriorID, "actual": actualID,
		"puntuacionAnterior": anteriorScore, "puntuacionActual": actualScore,
		"delta": actualScore - anteriorScore, "tendencia": tendencia, "casos": casos,
		"hallazgosResueltos": resueltos, "hallazgosNuevos": nuevos,
	})
}

func agregarUnicos(destino, valores []string) []string {
	vistos := make(map[string]struct{}, len(destino)+len(valores))
	for _, valor := range destino {
		vistos[strings.ToLower(strings.TrimSpace(valor))] = struct{}{}
	}
	for _, valor := range valores {
		clave := strings.ToLower(strings.TrimSpace(valor))
		if clave == "" {
			continue
		}
		if _, existe := vistos[clave]; existe {
			continue
		}
		vistos[clave] = struct{}{}
		destino = append(destino, valor)
	}
	return destino
}

func (s *Servidor) resultadosParaComparar(ctx context.Context, org, runID string) (int, map[string]resultadoCasoComparacion, error) {
	var score int
	if err := s.db.QueryRowContext(ctx, `SELECT score FROM agent_test_run WHERE organization_id=? AND id=? AND status='done' AND score IS NOT NULL`, org, runID).Scan(&score); err != nil {
		return 0, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT tc.persona,tc.hallazgos,s.scenario_id FROM agent_test_case tc JOIN lab_case_snapshot s ON s.case_id=tc.id AND s.organization_id=tc.organization_id WHERE tc.organization_id=? AND tc.run_id=? AND tc.status='done'`, org, runID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	resultado := make(map[string]resultadoCasoComparacion)
	for rows.Next() {
		var persona, escenarioID string
		var raw []byte
		if err := rows.Scan(&persona, &raw, &escenarioID); err != nil {
			return 0, nil, err
		}
		var evaluacion lab.ResultadoJuez
		if json.Unmarshal(raw, &evaluacion) != nil {
			continue
		}
		resultado[escenarioID] = resultadoCasoComparacion{EscenarioID: escenarioID, Persona: persona, Resultado: evaluacion}
	}
	return score, resultado, rows.Err()
}
