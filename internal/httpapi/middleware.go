package httpapi

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"crm-api/internal/identificadores"
)

type claveContexto string

const (
	claveRequestID claveContexto = "request_id"
	claveSesion    claveContexto = "sesion"
)

type estadoWriter struct {
	http.ResponseWriter
	estado int
	bytes  int
}

func (w *estadoWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *estadoWriter) WriteHeader(codigo int) {
	w.estado = codigo
	w.ResponseWriter.WriteHeader(codigo)
}

func (w *estadoWriter) Write(p []byte) (int, error) {
	if w.estado == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func middlewareBase(logger *slog.Logger, urlBase string, desarrollo bool, siguiente http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := identificadores.Nuevo("req")
		if err != nil {
			requestID = fmt.Sprintf("req_%d", time.Now().UnixNano())
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		inicio := time.Now()
		sw := &estadoWriter{ResponseWriter: w}
		ctx := context.WithValue(r.Context(), claveRequestID, requestID)
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recuperado", "request_id", requestID, "error", rec, "stack", string(debug.Stack()))
				if sw.estado == 0 {
					responderError(sw, http.StatusInternalServerError, "internal_error", "Error interno", nil)
				}
			}
			estado := sw.estado
			if estado == 0 {
				estado = http.StatusOK
			}
			logger.Info("peticion HTTP", "request_id", requestID, "metodo", r.Method, "ruta", r.URL.Path,
				"estado", estado, "bytes", sw.bytes, "duracion_ms", time.Since(inicio).Milliseconds())
		}()
		origen := r.Header.Get("Origin")
		if desarrollo && origen == "http://localhost:4200" {
			w.Header().Set("Access-Control-Allow-Origin", origen)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			w.Header().Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if metodoMutable(r.Method) && !(desarrollo && origen == "http://localhost:4200") && !origenPermitido(r, urlBase) {
			responderError(sw, http.StatusForbidden, "origin_not_allowed", "Origen no permitido", nil)
			return
		}
		siguiente.ServeHTTP(sw, r.WithContext(ctx))
	})
}

func metodoMutable(metodo string) bool {
	return metodo != http.MethodGet && metodo != http.MethodHead && metodo != http.MethodOptions
}

func origenPermitido(r *http.Request, base string) bool {
	origen := r.Header.Get("Origin")
	if origen == "" {
		return true
	}
	esperada, err := url.Parse(base)
	recibida, err2 := url.Parse(origen)
	if err != nil || err2 != nil {
		return false
	}
	a := strings.ToLower(esperada.Scheme + "://" + esperada.Host)
	b := strings.ToLower(recibida.Scheme + "://" + recibida.Host)
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
