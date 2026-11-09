package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxMedio = 25 << 20

func (s *Servidor) ObtenerMedio(w http.ResponseWriter, r *http.Request) {
	ses := sesionDe(r.Context())
	var ruta, mime, nombre sql.NullString
	err := s.db.QueryRowContext(r.Context(), "SELECT storage_path,mime_type,file_name FROM media_asset WHERE organization_id=? AND id=? AND fetch_status='available'", ses.OrganizacionID, r.PathValue("id")).Scan(&ruta, &mime, &nombre)
	if errors.Is(err, sql.ErrNoRows) {
		responderError(w, 404, "not_found", "Archivo no encontrado", nil)
		return
	}
	if err != nil {
		s.errorInterno(w, "leer archivo", err)
		return
	}
	base, _ := filepath.Abs(s.cfg.DirectorioMedios)
	archivo, _ := filepath.Abs(filepath.Join(base, filepath.FromSlash(ruta.String)))
	if !strings.HasPrefix(archivo, base+string(os.PathSeparator)) {
		responderError(w, 404, "not_found", "Archivo no encontrado", nil)
		return
	}
	if mime.Valid {
		w.Header().Set("Content-Type", mime.String)
	}
	if nombre.Valid {
		w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(filepath.Base(nombre.String), `"`, "")+`"`)
	}
	http.ServeFile(w, r, archivo)
}

func (s *Servidor) guardarMedioMeta(ctx context.Context, org, id, waID, nombre string) error {
	cred, err := s.credencialMeta(ctx, org)
	if err != nil {
		return err
	}
	m, err := s.clienteMeta(cred.Token, cred.Version).DescargarMedio(ctx, waID, maxMedio)
	if err != nil {
		_, _ = s.db.ExecContext(ctx, "UPDATE media_asset SET fetch_status='failed',fetch_error=? WHERE organization_id=? AND id=?", err.Error(), org, id)
		return err
	}
	ext := ".bin"
	if e, _ := mime.ExtensionsByType(m.MIME); len(e) > 0 {
		ext = e[0]
	}
	rel := filepath.ToSlash(filepath.Join(org, id+ext))
	abs := filepath.Join(s.cfg.DirectorioMedios, filepath.FromSlash(rel))
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(abs, m.Datos, 0600); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE media_asset SET mime_type=?,file_name=?,file_size=?,storage_path=?,fetch_status='available',fetch_error=NULL,updated_at=UTC_TIMESTAMP(3) WHERE organization_id=? AND id=?", m.MIME, filepath.Base(nombre), m.Tamano, rel, org, id)
	return err
}
