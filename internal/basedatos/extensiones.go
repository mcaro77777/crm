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
	const asignaciones = `CREATE TABLE IF NOT EXISTS conversation_assignment (
		organization_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		conversation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		user_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
		updated_at DATETIME(3) NOT NULL,
		PRIMARY KEY(organization_id,conversation_id),
		CONSTRAINT fk_assignment_org FOREIGN KEY(organization_id) REFERENCES organization(id) ON DELETE CASCADE,
		CONSTRAINT fk_assignment_conversation FOREIGN KEY(conversation_id) REFERENCES conversation(id) ON DELETE CASCADE,
		CONSTRAINT fk_assignment_user FOREIGN KEY(user_id) REFERENCES user(id) ON DELETE SET NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`
	if _, err := db.ExecContext(ctx, asignaciones); err != nil {
		return fmt.Errorf("crear asignaciones de conversaciones: %w", err)
	}
	const escenariosLaboratorio = `CREATE TABLE IF NOT EXISTS lab_scenario (
		id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		organization_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		nombre VARCHAR(160) NOT NULL,
		persona VARCHAR(255) NOT NULL,
		objetivo TEXT NOT NULL,
		turnos JSON NOT NULL,
		criterios JSON NOT NULL,
		activo TINYINT NOT NULL DEFAULT 1,
		created_at DATETIME(3) NOT NULL,
		updated_at DATETIME(3) NOT NULL,
		PRIMARY KEY (id),
		KEY lab_scenario_org_idx (organization_id, activo, updated_at),
		CONSTRAINT fk_lab_scenario_org FOREIGN KEY (organization_id) REFERENCES organization(id) ON DELETE CASCADE,
		CONSTRAINT lab_scenario_activo_ck CHECK (activo IN (0,1))
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`
	if _, err := db.ExecContext(ctx, escenariosLaboratorio); err != nil {
		return fmt.Errorf("crear escenarios de laboratorio: %w", err)
	}
	const snapshotsLaboratorio = `CREATE TABLE IF NOT EXISTS lab_case_snapshot (
		case_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		organization_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		scenario_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		nombre VARCHAR(160) NOT NULL,
		objetivo TEXT NOT NULL,
		turnos JSON NOT NULL,
		criterios JSON NOT NULL,
		created_at DATETIME(3) NOT NULL,
		PRIMARY KEY (case_id),
		KEY lab_case_snapshot_org_idx (organization_id, scenario_id),
		CONSTRAINT fk_lab_case_snapshot_case FOREIGN KEY (case_id) REFERENCES agent_test_case(id) ON DELETE CASCADE,
		CONSTRAINT fk_lab_case_snapshot_org FOREIGN KEY (organization_id) REFERENCES organization(id) ON DELETE CASCADE,
		CONSTRAINT fk_lab_case_snapshot_scenario FOREIGN KEY (scenario_id) REFERENCES lab_scenario(id) ON DELETE RESTRICT
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`
	if _, err := db.ExecContext(ctx, snapshotsLaboratorio); err != nil {
		return fmt.Errorf("crear snapshots de laboratorio: %w", err)
	}
	return nil
}
