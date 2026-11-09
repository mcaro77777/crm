package identificadores

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Nuevo devuelve un identificador opaco y seguro con el prefijo del dominio.
func Nuevo(prefijo string) (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generar aleatoriedad: %w", err)
	}
	return prefijo + "_" + base64.RawURLEncoding.EncodeToString(b), nil
}
