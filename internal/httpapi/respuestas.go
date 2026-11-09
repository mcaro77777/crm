package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type errorAPI struct {
	Error detalleError `json:"error"`
}

type detalleError struct {
	Codigo  string `json:"code"`
	Mensaje string `json:"message"`
	Detalle any    `json:"details,omitempty"`
}

func responderJSON(w http.ResponseWriter, estado int, dato any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(estado)
	if dato != nil {
		_ = json.NewEncoder(w).Encode(dato)
	}
}

func responderError(w http.ResponseWriter, estado int, codigo, mensaje string, detalle any) {
	responderJSON(w, estado, errorAPI{Error: detalleError{Codigo: codigo, Mensaje: mensaje, Detalle: detalle}})
}

func decodificarJSON(w http.ResponseWriter, r *http.Request, destino any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(destino); err != nil {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "JSON invalido", err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		responderError(w, http.StatusUnprocessableEntity, "validation_error", "El body debe contener un solo objeto JSON", nil)
		return false
	}
	return true
}
