package despacho

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const TimeoutEntrega = 20 * time.Second

type Destino struct{ URL, Secreto string }
type ResultadoEntrega struct {
	Correcta, Reintentable bool
	Detalle                string
}

type Entregador struct{ cliente *http.Client }

func NuevoEntregador() *Entregador {
	return &Entregador{cliente: &http.Client{Timeout: TimeoutEntrega, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (e *Entregador) Entregar(ctx context.Context, evento Evento, destino Destino) ResultadoEntrega {
	cuerpo, firma, err := SerializarYFirmar(evento, destino.Secreto)
	if err != nil {
		return ResultadoEntrega{Detalle: "no se pudo preparar el despacho"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destino.URL, bytes.NewReader(cuerpo))
	if err != nil {
		return ResultadoEntrega{Detalle: "la URL de despacho es invalida"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CabeceraFirma, firma)
	req.Header.Set(CabeceraDespacho, evento.DespachoID)
	org := evento.Organizacion.ID
	if evento.Organizacion.Slug != nil && *evento.Organizacion.Slug != "" {
		org = *evento.Organizacion.Slug
	}
	req.Header.Set(CabeceraOrganizacion, org)
	res, err := e.cliente.Do(req)
	if err != nil {
		return ResultadoEntrega{Reintentable: true, Detalle: "no se pudo conectar con el cerebro"}
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	clasificacion := ClasificarRespuesta(res.StatusCode)
	if clasificacion.Correcta {
		return ResultadoEntrega{Correcta: true}
	}
	return ResultadoEntrega{Reintentable: clasificacion.Reintentable, Detalle: fmt.Sprintf("el cerebro respondio HTTP %d", res.StatusCode)}
}
