package despacho

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"crm-api/internal/eventos"
	"crm-api/internal/handoff"
	"crm-api/internal/seguridad"
)

const intervaloBarrido = 2 * time.Second

type Worker struct {
	db           *sql.DB
	repo         *Repositorio
	handoffs     *handoff.Servicio
	llave        []byte
	bus          *eventos.Bus
	logger       *slog.Logger
	entregador   *Entregador
	procesador   Procesador
	concurrencia int
}

// Procesador ejecuta un turno integrado. La cola sigue siendo responsabilidad
// de Worker y el procesador nunca confirma ni reclama filas por su cuenta.
type Procesador func(context.Context, Evento) ResultadoEntrega

func NuevoWorker(db *sql.DB, llave []byte, bus *eventos.Bus, logger *slog.Logger) *Worker {
	return &Worker{db: db, repo: NuevoRepositorio(db), handoffs: handoff.Nuevo(db), llave: llave, bus: bus, logger: logger, entregador: NuevoEntregador(), concurrencia: 1}
}

func NuevoWorkerIntegrado(db *sql.DB, bus *eventos.Bus, logger *slog.Logger, concurrencia int, procesador Procesador) *Worker {
	if concurrencia < 1 {
		concurrencia = 1
	}
	return &Worker{db: db, repo: NuevoRepositorio(db), handoffs: handoff.Nuevo(db), bus: bus, logger: logger, procesador: procesador, concurrencia: concurrencia}
}

// Ejecutar barre el outbox hasta que el contexto se cancele. La durabilidad
// esta en MySQL: al reiniciar retoma pendientes y leases vencidos.
func (w *Worker) Ejecutar(ctx context.Context) {
	var grupo sync.WaitGroup
	for i := 0; i < w.concurrencia; i++ {
		grupo.Add(1)
		go func() { defer grupo.Done(); w.ejecutarHilo(ctx) }()
	}
	grupo.Wait()
}

func (w *Worker) ejecutarHilo(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		procesado, err := w.ProcesarUnaVez(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.Error("fallo al reclamar despacho", "error", err)
		}
		if procesado {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) tanda(ctx context.Context) {
	for i := 0; i < 10; i++ {
		procesado, err := w.ProcesarUnaVez(ctx)
		if err != nil {
			w.logger.Error("fallo al reclamar despacho", "error", err)
			return
		}
		if !procesado {
			return
		}
	}
}

// ProcesarUnaVez reclama y procesa como maximo un despacho. El servicio usa
// esta misma operacion en su barrido; exponerla permite un arnes determinista
// para releases sin duplicar la logica del worker.
func (w *Worker) ProcesarUnaVez(ctx context.Context) (bool, error) {
	fila, err := w.repo.TomarUno(ctx, time.Now().UTC())
	if err != nil || fila == nil {
		return false, err
	}
	w.publicarEstado(ctx, fila.OrganizacionID, fila.ConversacionID)
	w.procesar(ctx, *fila)
	return true, nil
}

func (w *Worker) procesar(ctx context.Context, fila Fila) {
	var destino Destino
	var err error
	if w.procesador == nil {
		destino, err = w.destino(ctx, fila.OrganizacionID)
		if errors.Is(err, ErrSinDestino) {
			_, _ = w.repo.Descartar(ctx, fila, err.Error(), time.Now().UTC())
			w.publicarEstado(ctx, fila.OrganizacionID, fila.ConversacionID)
			return
		}
		if err != nil {
			w.fallo(ctx, fila, true, "no se pudo leer la configuracion del cerebro")
			return
		}
	}
	evento, err := ConstruirEvento(ctx, w.db, fila, time.Now().UTC())
	if err != nil {
		if errors.Is(err, ErrConversacionAusente) || errors.Is(err, ErrIAPausada) || errors.Is(err, ErrSinMensajes) {
			_, _ = w.repo.Descartar(ctx, fila, err.Error(), time.Now().UTC())
			w.publicarEstado(ctx, fila.OrganizacionID, fila.ConversacionID)
			return
		}
		w.fallo(ctx, fila, true, "no se pudo construir el despacho")
		return
	}
	var resultado ResultadoEntrega
	ctxTurno, cancelarTurno := context.WithCancel(ctx)
	latidoTerminado := make(chan struct{})
	go w.mantenerLease(ctxTurno, cancelarTurno, fila, latidoTerminado)
	if w.procesador != nil {
		resultado = w.procesador(ctxTurno, evento)
	} else {
		resultado = w.entregador.Entregar(ctxTurno, evento, destino)
	}
	cancelarTurno()
	<-latidoTerminado
	propio, err := w.repo.RenovarLease(ctx, fila, time.Now().UTC())
	if err != nil || !propio {
		return
	}
	if resultado.Correcta {
		entregados := make([]string, 0, len(evento.Mensajes))
		for _, mensaje := range evento.Mensajes {
			entregados = append(entregados, mensaje.ID)
		}
		pendientes, propio, err := w.repo.FinalizarEntregado(ctx, fila, entregados, time.Now().UTC())
		if err != nil || !propio {
			return
		}
		if len(pendientes) > 0 {
			_, err = w.repo.Encolar(ctx, fila.OrganizacionID, fila.ConversacionID, pendientes, time.Now().UTC(), false)
			if err != nil {
				w.logger.Error("fallo al reencolar mensajes llegados durante el despacho", "conversacion", fila.ConversacionID, "error", err)
			}
		}
		w.publicarEstado(ctx, fila.OrganizacionID, fila.ConversacionID)
		return
	}
	w.fallo(ctx, fila, resultado.Reintentable, resultado.Detalle)
}

// mantenerLease impide que una llamada lenta al proveedor sea reclamada por
// otro worker. Si se pierde la propiedad cancela el turno en curso.
func (w *Worker) mantenerLease(ctx context.Context, cancelar context.CancelFunc, fila Fila, terminado chan<- struct{}) {
	defer close(terminado)
	ticker := time.NewTicker(Lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ahora := <-ticker.C:
			propio, err := w.repo.RenovarLease(ctx, fila, ahora.UTC())
			if err != nil || !propio {
				if err != nil && ctx.Err() == nil {
					w.logger.Error("fallo al renovar lease del despacho", "despacho", fila.ID, "error", err)
				}
				cancelar()
				return
			}
		}
	}
}

func (w *Worker) fallo(ctx context.Context, fila Fila, reintentable bool, detalle string) {
	caduca := false
	var err error
	if reintentable {
		caduca, err = w.repo.ReintentarOCaducar(ctx, fila, detalle, time.Now().UTC())
	} else {
		caduca, err = w.repo.Caducar(ctx, fila, detalle, time.Now().UTC())
	}
	if err != nil {
		w.logger.Error("fallo al actualizar despacho", "despacho", fila.ID, "error", err)
		return
	}
	w.publicarEstado(ctx, fila.OrganizacionID, fila.ConversacionID)
	if !caduca {
		return
	}
	estado, cambio, err := w.handoffs.Tomar(ctx, fila.OrganizacionID, fila.ConversacionID, "error", time.Now().UTC())
	if err != nil {
		w.logger.Error("fallo al traspasar conversacion", "conversacion", fila.ConversacionID, "error", err)
		return
	}
	if cambio {
		w.bus.Publicar(fila.OrganizacionID, "conversation.updated", map[string]any{"conversationId": fila.ConversacionID, "aiEnabled": estado.IAHabilitada, "handoffAt": estado.HandoffEn, "handoffReason": estado.Motivo})
	}
}

func (w *Worker) publicarEstado(ctx context.Context, organizacionID, conversacionID string) {
	estado, err := w.repo.Estado(ctx, organizacionID, conversacionID)
	if err != nil || estado == nil {
		return
	}
	w.bus.Publicar(organizacionID, "dispatch.updated", map[string]any{"conversationId": conversacionID, "dispatch": estado})
}

func (w *Worker) destino(ctx context.Context, organizacionID string) (Destino, error) {
	var url, cifrado, iv, tag sql.NullString
	err := w.db.QueryRowContext(ctx, `SELECT u.valor,k.secret_cipher,k.secret_iv,k.secret_tag FROM app_setting u JOIN app_setting k ON k.organization_id=u.organization_id AND k.clave='brain_api_key' WHERE u.organization_id=? AND u.clave='brain_dispatch_url'`, organizacionID).Scan(&url, &cifrado, &iv, &tag)
	if errors.Is(err, sql.ErrNoRows) || !url.Valid || !cifrado.Valid {
		return Destino{}, ErrSinDestino
	}
	if err != nil {
		return Destino{}, err
	}
	secreto, err := seguridad.DescifrarSecreto(w.llave, seguridad.SecretoCifrado{Cifrado: cifrado.String, IV: iv.String, Tag: tag.String})
	if err != nil {
		return Destino{}, err
	}
	if url.String == "" || len(secreto) < 16 {
		return Destino{}, ErrSinDestino
	}
	return Destino{URL: url.String, Secreto: secreto}, nil
}
