package configuracion

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Configuracion struct {
	Entorno                string
	Puerto                 string
	URLBase                string
	DSN                    string
	SecretoAutenticacion   string
	LlaveCifrado           []byte
	DuracionSesion         time.Duration
	MaxConexionesAbiertas  int
	MaxConexionesInactivas int
	VidaMaximaConexion     time.Duration
	DirectorioCliente      string
	DirectorioMedios       string
	MetaGraphBaseURL       string
}

func Cargar() (Configuracion, error) {
	cfg := Configuracion{
		Entorno:              valor("APP_ENV", "development"),
		Puerto:               valor("PORT", "8080"),
		URLBase:              valor("APP_BASE_URL", "http://localhost:8080"),
		DSN:                  strings.TrimSpace(os.Getenv("DATABASE_DSN")),
		SecretoAutenticacion: os.Getenv("AUTH_SECRET"),
		DirectorioCliente:    valor("CLIENT_APP_DIR", "ClientApp"),
		DirectorioMedios:     valor("MEDIA_DIR", ".dev-media"),
		MetaGraphBaseURL:     valor("META_GRAPH_BASE_URL", "https://graph.facebook.com"),
	}

	var err error
	cfg.DuracionSesion, err = duracionHoras("SESSION_HOURS", 168)
	if err != nil {
		return cfg, err
	}
	cfg.MaxConexionesAbiertas, err = entero("DB_MAX_OPEN_CONNS", 20)
	if err != nil {
		return cfg, err
	}
	cfg.MaxConexionesInactivas, err = entero("DB_MAX_IDLE_CONNS", 10)
	if err != nil {
		return cfg, err
	}
	minutos, err := entero("DB_CONN_MAX_LIFETIME_MINUTES", 30)
	if err != nil {
		return cfg, err
	}
	cfg.VidaMaximaConexion = time.Duration(minutos) * time.Minute

	if cfg.DSN == "" {
		return cfg, errors.New("DATABASE_DSN es obligatoria")
	}
	if len(cfg.SecretoAutenticacion) < 32 {
		return cfg, errors.New("AUTH_SECRET debe tener al menos 32 caracteres")
	}
	if _, err := url.ParseRequestURI(cfg.URLBase); err != nil {
		return cfg, fmt.Errorf("APP_BASE_URL invalida: %w", err)
	}
	if u, err := url.ParseRequestURI(cfg.MetaGraphBaseURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return cfg, errors.New("META_GRAPH_BASE_URL debe ser una URL HTTP o HTTPS valida")
	}
	if llave := strings.TrimSpace(os.Getenv("ENCRYPTION_KEY")); llave != "" {
		cfg.LlaveCifrado, err = base64.StdEncoding.DecodeString(llave)
		if err != nil || len(cfg.LlaveCifrado) != 32 {
			return cfg, errors.New("ENCRYPTION_KEY debe contener exactamente 32 bytes en base64")
		}
	} else if cfg.Entorno == "production" {
		return cfg, errors.New("ENCRYPTION_KEY es obligatoria en produccion")
	}
	return cfg, nil
}

func (c Configuracion) Produccion() bool { return c.Entorno == "production" }

func valor(nombre, defecto string) string {
	if v := strings.TrimSpace(os.Getenv(nombre)); v != "" {
		return v
	}
	return defecto
}

func entero(nombre string, defecto int) (int, error) {
	v := valor(nombre, strconv.Itoa(defecto))
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s debe ser un entero positivo", nombre)
	}
	return n, nil
}

func duracionHoras(nombre string, defecto int) (time.Duration, error) {
	horas, err := entero(nombre, defecto)
	if err != nil {
		return 0, err
	}
	return time.Duration(horas) * time.Hour, nil
}
