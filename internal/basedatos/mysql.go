package basedatos

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"crm-api/internal/configuracion"
	"github.com/go-sql-driver/mysql"
)

var tablasRequeridas = []string{
	"account", "ad_attribution", "agent_profile", "agent_test_case",
	"agent_test_run", "ai_credentials", "booking", "calendar_settings",
	"capi_settings", "contact", "conversation", "conversation_note",
	"conversion_event", "dispatch", "google_credentials", "instagram_credentials",
	"invitation", "kb_entry", "lead", "lead_stage_event", "media_asset",
	"member", "message", "messenger_credentials", "meta_credentials",
	"offered_slot", "organization", "pipeline_stage", "quick_reply", "session",
	"template", "tiktok_credentials", "user", "verification", "webhook_event",
	"zoom_credentials",
}

func Abrir(ctx context.Context, cfg configuracion.Configuracion) (*sql.DB, error) {
	dsn, err := prepararDSN(cfg.DSN)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir MySQL: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxConexionesAbiertas)
	db.SetMaxIdleConns(cfg.MaxConexionesInactivas)
	db.SetConnMaxLifetime(cfg.VidaMaximaConexion)

	pingCtx, cancelar := context.WithTimeout(ctx, 8*time.Second)
	defer cancelar()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("conectar con MySQL: %w", err)
	}
	if err := AsegurarExtensiones(pingCtx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := VerificarEsquema(pingCtx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func prepararDSN(dsn string) (string, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("DATABASE_DSN invalida: %w", err)
	}
	if cfg.DBName != "crm" {
		return "", fmt.Errorf("DATABASE_DSN debe apuntar a la base crm; apunta a %q", cfg.DBName)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 8 * time.Second
	cfg.ReadTimeout = 15 * time.Second
	cfg.WriteTimeout = 15 * time.Second
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.Params["time_zone"] = "'+00:00'"
	return cfg.FormatDSN(), nil
}

func VerificarEsquema(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'`)
	if err != nil {
		return fmt.Errorf("comprobar esquema crm: %w", err)
	}
	defer rows.Close()
	existentes := make(map[string]bool, len(tablasRequeridas))
	for rows.Next() {
		var nombre string
		if err := rows.Scan(&nombre); err != nil {
			return fmt.Errorf("leer tablas de crm: %w", err)
		}
		existentes[nombre] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("recorrer tablas de crm: %w", err)
	}
	faltantes := make([]string, 0)
	for _, nombre := range tablasRequeridas {
		if !existentes[nombre] {
			faltantes = append(faltantes, nombre)
		}
	}
	if len(faltantes) > 0 {
		sort.Strings(faltantes)
		return fmt.Errorf("esquema crm incompleto; faltan tablas: %s", strings.Join(faltantes, ", "))
	}
	return nil
}
