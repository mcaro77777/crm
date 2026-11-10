// Package canales declara las diferencias entre los canales conversacionales.
package canales

import "time"

type Capacidad struct {
	Ventana       time.Duration
	VentanaHumana time.Duration
	MaxCaracteres int
	MaxMensajes   int
	Plantillas    bool
}

var catalogo = map[string]Capacidad{
	"whatsapp":  {Ventana: 24 * time.Hour, MaxCaracteres: 4096, MaxMensajes: 20, Plantillas: true},
	"instagram": {Ventana: 24 * time.Hour, VentanaHumana: 7 * 24 * time.Hour, MaxCaracteres: 1000, MaxMensajes: 5},
	"messenger": {Ventana: 24 * time.Hour, VentanaHumana: 7 * 24 * time.Hour, MaxCaracteres: 2000, MaxMensajes: 5},
	"tiktok":    {Ventana: 48 * time.Hour, MaxCaracteres: 2000, MaxMensajes: 10},
}

func Obtener(canal string) (Capacidad, bool) { c, ok := catalogo[canal]; return c, ok }

func CatalogoPublico() map[string]map[string]any {
	salida := make(map[string]map[string]any, len(catalogo))
	for nombre, c := range catalogo {
		salida[nombre] = map[string]any{"ventanaHoras": int(c.Ventana.Hours()), "ventanaHumanaHoras": int(c.VentanaHumana.Hours()), "maxCaracteres": c.MaxCaracteres, "maxMensajes": c.MaxMensajes, "plantillas": c.Plantillas}
	}
	return salida
}
