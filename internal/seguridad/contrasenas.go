package seguridad

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	iteracionesPBKDF2 = 600_000
	bytesSal          = 16
	bytesClave        = 32
)

func HashContrasena(contrasena string) (string, error) {
	if len(contrasena) < 12 {
		return "", errors.New("la contrasena debe tener al menos 12 caracteres")
	}
	sal := make([]byte, bytesSal)
	if _, err := rand.Read(sal); err != nil {
		return "", fmt.Errorf("generar sal: %w", err)
	}
	clave := pbkdf2SHA256([]byte(contrasena), sal, iteracionesPBKDF2, bytesClave)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",
		iteracionesPBKDF2,
		base64.RawStdEncoding.EncodeToString(sal),
		base64.RawStdEncoding.EncodeToString(clave),
	), nil
}

func VerificarContrasena(contrasena, codificada string) bool {
	partes := strings.Split(codificada, "$")
	if len(partes) != 4 || partes[0] != "pbkdf2-sha256" {
		return false
	}
	iteraciones, err := strconv.Atoi(partes[1])
	if err != nil || iteraciones < 100_000 || iteraciones > 2_000_000 {
		return false
	}
	sal, err := base64.RawStdEncoding.DecodeString(partes[2])
	if err != nil || len(sal) < 16 {
		return false
	}
	esperada, err := base64.RawStdEncoding.DecodeString(partes[3])
	if err != nil || len(esperada) != bytesClave {
		return false
	}
	actual := pbkdf2SHA256([]byte(contrasena), sal, iteraciones, len(esperada))
	return subtle.ConstantTimeCompare(actual, esperada) == 1
}

func TokenSeguro(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generar token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashToken(token string) string {
	suma := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(suma[:])
}

func HashTokenConSecreto(token, secreto string) string {
	mac := hmac.New(sha256.New, []byte(secreto))
	mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func pbkdf2SHA256(contrasena, sal []byte, iteraciones, longitud int) []byte {
	tamano := sha256.Size
	bloques := (longitud + tamano - 1) / tamano
	resultado := make([]byte, 0, bloques*tamano)
	for bloque := 1; bloque <= bloques; bloque++ {
		mac := hmac.New(sha256.New, contrasena)
		mac.Write(sal)
		mac.Write([]byte{byte(bloque >> 24), byte(bloque >> 16), byte(bloque >> 8), byte(bloque)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iteraciones; i++ {
			mac = hmac.New(sha256.New, contrasena)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		resultado = append(resultado, t...)
	}
	return resultado[:longitud]
}
