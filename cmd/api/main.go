package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"crm-api/internal/basedatos"
	"crm-api/internal/configuracion"
	"crm-api/internal/despacho"
	"crm-api/internal/eventos"
	"crm-api/internal/httpapi"
	"crm-api/internal/version"
	"crm-api/internal/webhooks"
)

func main() {
	if err := ejecutar(); err != nil {
		fmt.Fprintln(os.Stderr, "crm-api:", err)
		os.Exit(1)
	}
}

func ejecutar() error {
	comando := "serve"
	if len(os.Args) > 1 {
		comando = os.Args[1]
	}
	if comando == "version" {
		fmt.Printf("crm-api %s (%s)\n", version.Version, version.Commit)
		return nil
	}
	if comando != "serve" && comando != "check-db" {
		return fmt.Errorf("comando desconocido %q; use serve, check-db o version", comando)
	}

	cfg, err := configuracion.Cargar()
	if err != nil {
		return err
	}
	logger := nuevoLogger(cfg.Produccion())
	ctx := context.Background()
	db, err := basedatos.Abrir(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if comando == "check-db" {
		fmt.Println("Base crm accesible y esquema completo (36 tablas).")
		return nil
	}

	bus := eventos.NuevoBus()
	contextoWorker, detenerWorker := context.WithCancel(context.Background())
	worker := despacho.NuevoWorker(db, cfg.LlaveCifrado, bus, logger)
	go worker.Ejecutar(contextoWorker)
	manejador, procesarWebhook := httpapi.NuevoServidorConProcesador(cfg, db, logger, bus)
	workerWebhooks := webhooks.NuevoWorker(db, procesarWebhook, logger)
	go workerWebhooks.Ejecutar(contextoWorker)
	defer detenerWorker()

	servidor := &http.Server{
		Addr:              ":" + cfg.Puerto,
		Handler:           manejador,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// SSE mantiene conexiones largas; cada acceso a BD tiene sus propios timeouts.
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	senales, detener := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer detener()
	canalError := make(chan error, 1)
	go func() {
		logger.Info("servidor iniciado", "puerto", cfg.Puerto, "entorno", cfg.Entorno, "version", version.Version)
		canalError <- servidor.ListenAndServe()
	}()

	select {
	case err := <-canalError:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("servidor HTTP: %w", err)
		}
	case <-senales.Done():
		logger.Info("apagando servidor")
		detenerWorker()
		ctxCierre, cancelar := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelar()
		if err := servidor.Shutdown(ctxCierre); err != nil {
			return fmt.Errorf("apagado ordenado: %w", err)
		}
	}
	return nil
}

func nuevoLogger(produccion bool) *slog.Logger {
	opciones := &slog.HandlerOptions{Level: slog.LevelInfo}
	if produccion {
		return slog.New(slog.NewJSONHandler(os.Stdout, opciones))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opciones))
}
