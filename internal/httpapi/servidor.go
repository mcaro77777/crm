package httpapi

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"crm-api/internal/configuracion"
	"crm-api/internal/despacho"
	"crm-api/internal/eventos"
	"crm-api/internal/handoff"
	"crm-api/internal/version"
)

type Servidor struct {
	cfg       configuracion.Configuracion
	db        *sql.DB
	logger    *slog.Logger
	bus       *eventos.Bus
	limite    *limitadorIntentos
	despachos *despacho.Repositorio
	handoffs  *handoff.Servicio
	limiteBot *limitadorIntentos
	fallosBot *limitadorIntentos
}

func NuevoServidor(cfg configuracion.Configuracion, db *sql.DB, logger *slog.Logger) http.Handler {
	return NuevoServidorConBus(cfg, db, logger, eventos.NuevoBus())
}

func NuevoServidorConBus(cfg configuracion.Configuracion, db *sql.DB, logger *slog.Logger, bus *eventos.Bus) http.Handler {
	s := &Servidor{cfg: cfg, db: db, logger: logger, bus: bus, limite: nuevoLimitadorIntentos(10, 10*time.Minute), despachos: despacho.NuevoRepositorio(db), handoffs: handoff.Nuevo(db), limiteBot: nuevoLimitadorIntentos(1200, time.Minute), fallosBot: nuevoLimitadorIntentos(30, time.Minute)}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.salud)
	mux.HandleFunc("GET /api/version", s.version)
	mux.Handle("POST /api/auth/register", s.limitarAutenticacion(http.HandlerFunc(s.registrar)))
	mux.Handle("POST /api/auth/login", s.limitarAutenticacion(http.HandlerFunc(s.iniciarSesion)))
	mux.Handle("POST /api/auth/logout", s.requerirSesion(http.HandlerFunc(s.cerrarSesion)))
	mux.Handle("GET /api/auth/session", s.requerirSesion(http.HandlerFunc(s.sesionActual)))

	mux.Handle("GET /api/contacts", s.requerirSesion(http.HandlerFunc(s.listarContactos)))
	mux.Handle("POST /api/contacts", s.requerirSesion(http.HandlerFunc(s.crearContacto)))
	mux.Handle("GET /api/contacts/{id}", s.requerirSesion(http.HandlerFunc(s.obtenerContacto)))
	mux.Handle("PATCH /api/contacts/{id}", s.requerirSesion(http.HandlerFunc(s.actualizarContacto)))

	mux.Handle("GET /api/pipeline", s.requerirSesion(http.HandlerFunc(s.obtenerPipeline)))
	mux.Handle("PATCH /api/leads/{id}/stage", s.requerirSesion(http.HandlerFunc(s.moverLead)))

	mux.Handle("GET /api/conversations", s.requerirSesion(http.HandlerFunc(s.listarConversaciones)))
	mux.Handle("GET /api/conversations/{id}", s.requerirSesion(http.HandlerFunc(s.obtenerConversacion)))
	mux.Handle("GET /api/conversations/{id}/messages", s.requerirSesion(http.HandlerFunc(s.listarMensajes)))
	mux.Handle("POST /api/conversations/{id}/messages", s.requerirSesion(http.HandlerFunc(s.enviarMensaje)))
	mux.Handle("POST /api/conversations/{id}/read", s.requerirSesion(http.HandlerFunc(s.marcarConversacionLeida)))
	mux.Handle("PATCH /api/conversations/{id}/agent", s.requerirSesion(http.HandlerFunc(s.cambiarResponsableConversacion)))
	mux.Handle("GET /api/events", s.requerirSesion(http.HandlerFunc(s.eventosSSE)))

	mux.Handle("GET /api/settings/whatsapp", s.requerirSesion(http.HandlerFunc(s.obtenerAjustesWhatsApp)))
	mux.Handle("PUT /api/settings/whatsapp", s.requerirSesion(http.HandlerFunc(s.guardarAjustesWhatsApp)))
	mux.Handle("POST /api/settings/whatsapp/test", s.requerirSesion(http.HandlerFunc(s.probarWhatsApp)))
	mux.Handle("DELETE /api/settings/whatsapp", s.requerirSesion(http.HandlerFunc(s.eliminarAjustesWhatsApp)))
	mux.Handle("GET /api/settings/ai", s.requerirSesion(http.HandlerFunc(s.obtenerIA)))
	mux.Handle("PUT /api/settings/ai", s.requerirSesion(http.HandlerFunc(s.guardarIA)))
	mux.Handle("POST /api/settings/ai/test", s.requerirSesion(http.HandlerFunc(s.probarIA)))
	mux.Handle("GET /api/settings/brain", s.requerirSesion(http.HandlerFunc(s.obtenerCerebro)))
	mux.Handle("PUT /api/settings/brain", s.requerirSesion(http.HandlerFunc(s.guardarCerebro)))
	mux.Handle("POST /api/settings/brain/test", s.requerirSesion(http.HandlerFunc(s.probarCerebro)))
	mux.Handle("GET /api/agent", s.requerirSesion(http.HandlerFunc(s.obtenerAgente)))
	mux.Handle("PATCH /api/agent", s.requerirSesion(http.HandlerFunc(s.guardarAgente)))
	mux.Handle("GET /api/kb", s.requerirSesion(http.HandlerFunc(s.listarConocimiento)))
	mux.Handle("POST /api/kb", s.requerirSesion(http.HandlerFunc(s.crearConocimiento)))
	mux.Handle("PATCH /api/kb/{id}", s.requerirSesion(http.HandlerFunc(s.actualizarConocimiento)))
	mux.Handle("DELETE /api/kb/{id}", s.requerirSesion(http.HandlerFunc(s.eliminarConocimiento)))

	mux.Handle("GET /api/bot/profile", s.requerirBot(http.HandlerFunc(s.perfilBot)))
	mux.Handle("GET /api/bot/context", s.requerirBot(http.HandlerFunc(s.contextoBot)))
	mux.Handle("POST /api/bot/messages", s.requerirBot(http.HandlerFunc(s.mensajeBot)))
	mux.Handle("POST /api/bot/handoff", s.requerirBot(http.HandlerFunc(s.handoffBot)))
	mux.Handle("POST /api/bot/llm/{ruta...}", s.requerirBot(http.HandlerFunc(s.proxyLLM)))

	mux.HandleFunc("GET /api/webhooks/wa/{token}", s.verificarWebhookWhatsApp)
	mux.HandleFunc("POST /api/webhooks/wa/{token}", s.recibirWebhookWhatsApp)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		responderError(w, http.StatusNotFound, "not_found", "Ruta API no encontrada", nil)
	})
	mux.Handle("/", manejadorSPA(cfg.DirectorioCliente))
	return middlewareBase(logger, cfg.URLBase, !cfg.Produccion(), mux)
}

func (s *Servidor) salud(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.db.PingContext(ctx); err != nil {
		responderError(w, http.StatusServiceUnavailable, "db_unavailable", "Base de datos no disponible", nil)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": version.Version, "commit": version.Commit, "database": "ok",
	})
}

func (s *Servidor) version(w http.ResponseWriter, _ *http.Request) {
	responderJSON(w, http.StatusOK, map[string]string{"version": version.Version, "commit": version.Commit})
}

func manejadorSPA(directorio string) http.Handler {
	indice := filepath.Join(directorio, "index.html")
	_, errorIndice := os.Stat(indice)
	archivos := http.FileServer(http.Dir(directorio))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if errorIndice != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("<!doctype html><html><body><h1>CRM API activa</h1><p>Falta copiar Angular compilado a ClientApp.</p></body></html>"))
			return
		}
		relativa := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		ruta := filepath.Join(directorio, filepath.FromSlash(relativa))
		if info, err := os.Stat(ruta); err == nil && !info.IsDir() {
			if strings.Contains(filepath.Base(ruta), ".") && !strings.HasSuffix(ruta, "index.html") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			archivos.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, indice)
	})
}
