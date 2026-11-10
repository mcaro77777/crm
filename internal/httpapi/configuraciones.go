package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"crm-api/internal/seguridad"
)

func (s *Servidor) guardarAjuste(ctx context.Context, organizacionID, clave, valor string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_setting (organization_id,clave,valor,updated_at)
		VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE valor=VALUES(valor),secret_cipher=NULL,secret_iv=NULL,secret_tag=NULL,updated_at=VALUES(updated_at)`, organizacionID, clave, valor, time.Now().UTC())
	return err
}

func (s *Servidor) guardarSecreto(ctx context.Context, organizacionID, clave, valor string) error {
	secreto, err := seguridad.CifrarSecreto(s.cfg.LlaveCifrado, valor)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_setting (organization_id,clave,secret_cipher,secret_iv,secret_tag,updated_at)
		VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE valor=NULL,secret_cipher=VALUES(secret_cipher),secret_iv=VALUES(secret_iv),secret_tag=VALUES(secret_tag),updated_at=VALUES(updated_at)`, organizacionID, clave, secreto.Cifrado, secreto.IV, secreto.Tag, time.Now().UTC())
	return err
}

func (s *Servidor) leerAjuste(ctx context.Context, organizacionID, clave, defecto string) string {
	var valor sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT valor FROM app_setting WHERE organization_id=? AND clave=?", organizacionID, clave).Scan(&valor); err != nil || !valor.Valid {
		return defecto
	}
	return valor.String
}

func (s *Servidor) leerSecreto(ctx context.Context, organizacionID, clave string) (string, error) {
	var cifrado, iv, tag string
	err := s.db.QueryRowContext(ctx, "SELECT secret_cipher,secret_iv,secret_tag FROM app_setting WHERE organization_id=? AND clave=?", organizacionID, clave).Scan(&cifrado, &iv, &tag)
	if err != nil {
		return "", err
	}
	return seguridad.DescifrarSecreto(s.cfg.LlaveCifrado, seguridad.SecretoCifrado{Cifrado: cifrado, IV: iv, Tag: tag})
}

func (s *Servidor) tokenWebhook(organizacionID string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.SecretoAutenticacion))
	_, _ = mac.Write([]byte("whatsapp:" + organizacionID))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func (s *Servidor) resolverOrganizacionWebhook(ctx context.Context, token string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT organization_id FROM meta_credentials
		UNION SELECT organization_id FROM app_setting WHERE clave='whatsapp_source' AND valor='zernio'`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if hmac.Equal([]byte(strings.ToLower(token)), []byte(s.tokenWebhook(id))) {
			return id, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", errors.New("webhook desconocido")
}
