package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Cliente struct {
	Token   string
	Version string
	BaseURL string
	HTTP    *http.Client
}

func Nuevo(token, version string) *Cliente {
	return NuevoConBaseURL(token, version, "https://graph.facebook.com")
}

// NuevoConBaseURL conserva Graph como valor productivo y permite que los
// arneses integrales sustituyan exclusivamente el transporte HTTP.
func NuevoConBaseURL(token, version, baseURL string) *Cliente {
	if version == "" {
		version = "v25.0"
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://graph.facebook.com"
	}
	return &Cliente{Token: token, Version: version, BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Cliente) Ejecutar(ctx context.Context, metodo, ruta string, entrada, salida any) error {
	var cuerpo io.Reader
	if entrada != nil {
		datos, err := json.Marshal(entrada)
		if err != nil {
			return err
		}
		cuerpo = bytes.NewReader(datos)
	}
	u := c.BaseURL + "/" + strings.Trim(c.Version, "/") + "/" + strings.TrimLeft(ruta, "/")
	req, err := http.NewRequestWithContext(ctx, metodo, u, cuerpo)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if entrada != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Meta no responde: %w", err)
	}
	defer res.Body.Close()
	datos, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var detalle struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(datos, &detalle)
		if detalle.Error.Message == "" {
			detalle.Error.Message = string(datos)
		}
		return fmt.Errorf("Meta respondio %d: %s", res.StatusCode, detalle.Error.Message)
	}
	if salida != nil && len(datos) > 0 {
		if err := json.Unmarshal(datos, salida); err != nil {
			return fmt.Errorf("respuesta invalida de Meta: %w", err)
		}
	}
	return nil
}

type Telefono struct {
	ID                 string `json:"id"`
	DisplayPhoneNumber string `json:"display_phone_number"`
	VerifiedName       string `json:"verified_name"`
}

func (c *Cliente) ConsultarTelefono(ctx context.Context, id string) (Telefono, error) {
	var salida Telefono
	ruta := url.PathEscape(id) + "?fields=id,display_phone_number,verified_name"
	err := c.Ejecutar(ctx, http.MethodGet, ruta, nil, &salida)
	return salida, err
}

func (c *Cliente) EnviarTexto(ctx context.Context, telefonoID, destinatario, texto string) (string, error) {
	entrada := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": destinatario, "type": "text", "text": map[string]any{"preview_url": false, "body": texto}}
	var salida struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.Ejecutar(ctx, http.MethodPost, url.PathEscape(telefonoID)+"/messages", entrada, &salida); err != nil {
		return "", err
	}
	if len(salida.Messages) == 0 || salida.Messages[0].ID == "" {
		return "", fmt.Errorf("Meta no devolvio el identificador del mensaje")
	}
	return salida.Messages[0].ID, nil
}

type Plantilla struct {
	ID          string `json:"id"`
	Nombre      string `json:"name"`
	Idioma      string `json:"language"`
	Categoria   string `json:"category"`
	Estado      string `json:"status"`
	Componentes []struct {
		Tipo  string `json:"type"`
		Texto string `json:"text"`
	} `json:"components"`
}

func (c *Cliente) ListarPlantillas(ctx context.Context, wabaID string) ([]Plantilla, error) {
	var salida struct {
		Data []Plantilla `json:"data"`
	}
	err := c.Ejecutar(ctx, http.MethodGet, url.PathEscape(wabaID)+"/message_templates?limit=250", nil, &salida)
	return salida.Data, err
}

func (c *Cliente) EnviarPlantilla(ctx context.Context, telefonoID, destinatario, nombre, idioma string) (string, error) {
	entrada := map[string]any{"messaging_product": "whatsapp", "to": destinatario, "type": "template", "template": map[string]any{"name": nombre, "language": map[string]string{"code": idioma}}}
	var salida struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.Ejecutar(ctx, http.MethodPost, url.PathEscape(telefonoID)+"/messages", entrada, &salida); err != nil {
		return "", err
	}
	if len(salida.Messages) == 0 {
		return "", fmt.Errorf("Meta no devolvio el identificador del mensaje")
	}
	return salida.Messages[0].ID, nil
}

type Medio struct {
	URL, MIME string
	Tamano    int64
	Datos     []byte
}

func (c *Cliente) DescargarMedio(ctx context.Context, id string, max int64) (Medio, error) {
	var info struct {
		URL    string `json:"url"`
		MIME   string `json:"mime_type"`
		Tamano int64  `json:"file_size"`
	}
	if err := c.Ejecutar(ctx, http.MethodGet, url.PathEscape(id), nil, &info); err != nil {
		return Medio{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	if err != nil {
		return Medio{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Medio{}, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return Medio{}, fmt.Errorf("descarga Meta respondio %d", res.StatusCode)
	}
	datos, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return Medio{}, err
	}
	if int64(len(datos)) > max {
		return Medio{}, fmt.Errorf("archivo supera el limite de %d bytes", max)
	}
	return Medio{URL: info.URL, MIME: info.MIME, Tamano: int64(len(datos)), Datos: datos}, nil
}
