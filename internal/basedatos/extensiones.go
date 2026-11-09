package basedatos

import (
	"context"
	"database/sql"
	"fmt"
)

// AsegurarExtensiones agrega almacenamiento para configuraciones administrables
// desde Nexora sin exigir pasos manuales al actualizar una instalación.
func AsegurarExtensiones(ctx context.Context, db *sql.DB) error {
	const consulta = `CREATE TABLE IF NOT EXISTS app_setting (
		organization_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		clave VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		valor TEXT NULL,
		secret_cipher LONGTEXT NULL,
		secret_iv VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NULL,
		secret_tag VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NULL,
		updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
		PRIMARY KEY (organization_id, clave),
		CONSTRAINT fk_app_setting_org FOREIGN KEY (organization_id)
			REFERENCES organization(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`
	if _, err := db.ExecContext(ctx, consulta); err != nil {
		return fmt.Errorf("crear almacenamiento de configuracion: %w", err)
	}
	return nil
}
