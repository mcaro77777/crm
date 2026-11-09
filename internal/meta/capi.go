package meta

import (
	"context"
	"net/http"
	"net/url"
)

// EnviarEventosCAPI mantiene todo el tráfico hacia Graph dentro del cliente
// central. El token viaja como Bearer y nunca forma parte de logs ni respuestas.
func (c *Cliente) EnviarEventosCAPI(ctx context.Context, datasetID string, eventos any) (string, error) {
	var salida struct {
		FBTraceID string `json:"fbtrace_id"`
	}
	err := c.Ejecutar(ctx, http.MethodPost, url.PathEscape(datasetID)+"/events", map[string]any{"data": eventos}, &salida)
	return salida.FBTraceID, err
}
