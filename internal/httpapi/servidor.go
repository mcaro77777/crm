package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"crm-api/internal/cerebro"
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
	cerebro   *cerebro.Servicio
}

func NuevoServidor(cfg configuracion.Configuracion, db *sql.DB, logger *slog.Logger) http.Handler {
	return NuevoServidorConBus(cfg, db, logger, eventos.NuevoBus())
}

func NuevoServidorConBus(cfg configuracion.Configuracion, db *sql.DB, logger *slog.Logger, bus *eventos.Bus) http.Handler {
	manejador, _ := NuevoServidorConProcesador(cfg, db, logger, bus)
	return manejador
}

// NuevoServidorConProcesador entrega, ademas del HTTP, la funcion que consume
// el worker durable de webhooks. Ambos comparten exactamente el mismo dominio.
func NuevoServidorConProcesador(cfg configuracion.Configuracion, db *sql.DB, logger *slog.Logger, bus *eventos.Bus) (http.Handler, func(context.Context, string, string, []byte) error) {
	manejador, webhooks, _ := NuevoServidorIntegrado(cfg, db, logger, bus)
	return manejador, webhooks
}

func NuevoServidorIntegrado(cfg configuracion.Configuracion, db *sql.DB, logger *slog.Logger, bus *eventos.Bus) (http.Handler, func(context.Context, string, string, []byte) error, despacho.Procesador) {
	s := &Servidor{cfg: cfg, db: db, logger: logger, bus: bus, limite: nuevoLimitadorIntentos(10, 10*time.Minute), despachos: despacho.NuevoRepositorio(db), handoffs: handoff.Nuevo(db), limiteBot: nuevoLimitadorIntentos(1200, time.Minute), fallosBot: nuevoLimitadorIntentos(30, time.Minute), cerebro: cerebro.Nuevo(db, cfg.LlaveCifrado)}
	mux := http.NewServeMux()
	s.registrarRutasLaboratorio(mux)
	s.registrarRutasAgenda(mux)

	mux.HandleFunc("GET /api/health", s.salud)
	mux.HandleFunc("GET /api/version", s.version)
	mux.Handle("POST /api/auth/register", s.limitarAutenticacion(http.HandlerFunc(s.registrar)))
	mux.Handle("POST /api/auth/login", s.limitarAutenticacion(http.HandlerFunc(s.iniciarSesion)))
	mux.Handle("POST /api/auth/logout", s.requerirSesion(http.HandlerFunc(s.cerrarSesion)))
	mux.Handle("GET /api/auth/session", s.requerirSesion(http.HandlerFunc(s.sesionActual)))

	mux.Handle("GET /api/contacts", s.requerirSesion(http.HandlerFunc(s.listarContactos)))
	mux.Handle("POST /api/contacts", s.requerirSesion(http.HandlerFunc(s.crearContacto)))
	mux.Handle("GET /api/contacts/{id}", s.requerirSesion(http.HandlerFunc(s.obtenerContacto)))
	mux.Handle("GET /api/contacts/{id}/ficha", s.requerirSesion(http.HandlerFunc(s.obtenerFichaContacto)))
	mux.Handle("PATCH /api/contacts/{id}/ficha", s.requerirSesion(http.HandlerFunc(s.guardarFichaContacto)))
	mux.Handle("POST /api/contacts/import", s.requerirSesion(http.HandlerFunc(s.importarContactosCSV)))
	mux.Handle("GET /api/contacts/export", s.requerirSesion(http.HandlerFunc(s.exportarContactosCSV)))
	mux.Handle("PATCH /api/contacts/{id}", s.requerirSesion(http.HandlerFunc(s.actualizarContacto)))

	mux.Handle("GET /api/pipeline", s.requerirSesion(http.HandlerFunc(s.obtenerPipeline)))
	mux.Handle("PATCH /api/leads/{id}/stage", s.requerirSesion(http.HandlerFunc(s.moverLead)))

	mux.Handle("GET /api/conversations", s.requerirSesion(http.HandlerFunc(s.listarConversaciones)))
	mux.Handle("GET /api/conversations/{id}", s.requerirSesion(http.HandlerFunc(s.obtenerConversacion)))
	mux.Handle("GET /api/conversations/{id}/messages", s.requerirSesion(http.HandlerFunc(s.listarMensajes)))
	mux.Handle("GET /api/conversations/{id}/dispatch", s.requerirSesion(http.HandlerFunc(s.estadoDespachoConversacion)))
	mux.Handle("POST /api/conversations/{id}/messages", s.requerirSesion(http.HandlerFunc(s.enviarMensaje)))
	mux.Handle("POST /api/conversations/{id}/read", s.requerirSesion(http.HandlerFunc(s.marcarConversacionLeida)))
	mux.Handle("PATCH /api/conversations/{id}/agent", s.requerirSesion(http.HandlerFunc(s.cambiarResponsableConversacion)))
	mux.Handle("PATCH /api/conversations/{id}/assignment", s.requerirSesion(http.HandlerFunc(s.asignarConversacion)))
	mux.Handle("POST /api/conversations/{id}/template", s.requerirSesion(http.HandlerFunc(s.EnviarPlantilla)))
	mux.Handle("GET /api/conversations/{id}/notes", s.requerirSesion(http.HandlerFunc(s.listarNotas)))
	mux.Handle("POST /api/conversations/{id}/notes", s.requerirSesion(http.HandlerFunc(s.crearNota)))
	mux.Handle("GET /api/events", s.requerirSesion(http.HandlerFunc(s.eventosSSE)))
	mux.Handle("GET /api/quick-replies", s.requerirSesion(http.HandlerFunc(s.listarRespuestasRapidas)))
	mux.Handle("POST /api/quick-replies", s.requerirSesion(http.HandlerFunc(s.crearRespuestaRapida)))
	mux.Handle("PATCH /api/quick-replies/{id}", s.requerirSesion(http.HandlerFunc(s.actualizarRespuestaRapida)))
	mux.Handle("DELETE /api/quick-replies/{id}", s.requerirSesion(http.HandlerFunc(s.eliminarRespuestaRapida)))
	mux.Handle("GET /api/media/{id}", s.requerirSesion(http.HandlerFunc(s.ObtenerMedio)))
	mux.Handle("GET /api/templates", s.requerirSesion(http.HandlerFunc(s.ListarPlantillas)))
	mux.Handle("POST /api/templates/sync", s.requerirSesion(http.HandlerFunc(s.SincronizarPlantillas)))
	mux.Handle("GET /api/search", s.requerirSesion(http.HandlerFunc(s.buscarGlobal)))
	mux.Handle("GET /api/analytics", s.requerirSesion(http.HandlerFunc(s.obtenerAnalitica)))
	mux.Handle("GET /api/analytics/export.csv", s.requerirSesion(http.HandlerFunc(s.exportarAnaliticaCSV)))
	mux.Handle("GET /api/settings/capi", s.requerirSesion(http.HandlerFunc(s.obtenerCAPI)))
	mux.Handle("PUT /api/settings/capi", s.requerirSesion(http.HandlerFunc(s.guardarCAPI)))
	mux.Handle("GET /api/settings/team", s.requerirSesion(http.HandlerFunc(s.listarEquipo)))
	mux.Handle("POST /api/settings/team/invitations", s.requerirSesion(http.HandlerFunc(s.invitarEquipo)))
	mux.Handle("POST /api/invitations/{id}/accept", s.requerirSesion(http.HandlerFunc(s.aceptarInvitacion)))
	mux.Handle("PATCH /api/settings/team/members/{id}", s.requerirSesion(http.HandlerFunc(s.cambiarRolEquipo)))

	mux.Handle("GET /api/settings/whatsapp", s.requerirSesion(http.HandlerFunc(s.obtenerAjustesWhatsApp)))
	mux.Handle("PUT /api/settings/whatsapp", s.requerirSesion(http.HandlerFunc(s.guardarAjustesWhatsApp)))
	mux.Handle("POST /api/settings/whatsapp/test", s.requerirSesion(http.HandlerFunc(s.probarWhatsApp)))
	mux.Handle("DELETE /api/settings/whatsapp", s.requerirSesion(http.HandlerFunc(s.eliminarAjustesWhatsApp)))
	mux.Handle("GET /api/settings/channels", s.requerirSesion(http.HandlerFunc(s.listarAjustesCanales)))
	mux.Handle("PUT /api/settings/channels/{canal}", s.requerirSesion(http.HandlerFunc(s.guardarAjustesCanal)))
	mux.Handle("POST /api/settings/channels/{canal}/test", s.requerirSesion(http.HandlerFunc(s.probarAjustesCanal)))
	mux.Handle("DELETE /api/settings/channels/{canal}", s.requerirSesion(http.HandlerFunc(s.eliminarAjustesCanal)))
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
	mux.HandleFunc("GET /api/webhooks/{canal}/{token}", s.verificarWebhookSocial)
	mux.HandleFunc("POST /api/webhooks/{canal}/{token}", s.recibirWebhookSocial)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		responderError(w, http.StatusNotFound, "not_found", "Ruta API no encontrada", nil)
	})
	mux.Handle("/", manejadorSPA(cfg.DirectorioCliente))
	return middlewareBase(logger, cfg.URLBase, !cfg.Produccion(), mux), s.procesarWebhookGuardado, s.procesarTurnoIntegrado
}

func (s *Servidor) procesarWebhookGuardado(ctx context.Context, organizacionID, fuente string, payload []byte) error {
	switch fuente {
	case "whatsapp":
		return s.procesarEventoWhatsApp(ctx, organizacionID, payload)
	case "meta_instagram":
		return s.procesarEventoMetaSocial(ctx, organizacionID, "instagram", payload)
	case "meta_messenger":
		return s.procesarEventoMetaSocial(ctx, organizacionID, "messenger", payload)
	case "zernio":
		return s.procesarEventoZernio(ctx, organizacionID, payload)
	default:
		return fmt.Errorf("fuente de webhook no soportada: %s", fuente)
	}
}

func (s *Servidor) salud(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.db.PingContext(ctx); err != nil {
		responderError(w, http.StatusServiceUnavailable, "db_unavailable", "Base de datos no disponible", nil)
		return
	}
	var pendientes, agotados int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(status IN ('pendiente','en_proceso')),0),COALESCE(SUM(status='agotado'),0) FROM webhook_event`).Scan(&pendientes, &agotados); err != nil {
		s.logger.Warn("no se pudo contar el inbox de webhooks", "error", err)
	}
	responderJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": version.Version, "commit": version.Commit, "database": "ok",
		"webhooks": map[string]int{"pendientes": pendientes, "agotados": agotados},
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
