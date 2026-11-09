package seguridad

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// SecretoCifrado representa un valor protegido con AES-256-GCM.
type SecretoCifrado struct {
	Cifrado string
	IV      string
	Tag     string
}

func CifrarSecreto(llave []byte, texto string) (SecretoCifrado, error) {
	if len(llave) != 32 {
		return SecretoCifrado{}, errors.New("la llave de cifrado debe tener 32 bytes")
	}
	bloque, err := aes.NewCipher(llave)
	if err != nil {
		return SecretoCifrado{}, err
	}
	gcm, err := cipher.NewGCM(bloque)
	if err != nil {
		return SecretoCifrado{}, err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return SecretoCifrado{}, err
	}
	sellado := gcm.Seal(nil, iv, []byte(texto), nil)
	corte := len(sellado) - gcm.Overhead()
	return SecretoCifrado{
		Cifrado: base64.StdEncoding.EncodeToString(sellado[:corte]),
		IV:      base64.StdEncoding.EncodeToString(iv),
		Tag:     base64.StdEncoding.EncodeToString(sellado[corte:]),
	}, nil
}

func DescifrarSecreto(llave []byte, secreto SecretoCifrado) (string, error) {
	if len(llave) != 32 {
		return "", errors.New("la llave de cifrado debe tener 32 bytes")
	}
	cifrado, err := base64.StdEncoding.DecodeString(secreto.Cifrado)
	if err != nil {
		return "", err
	}
	iv, err := base64.StdEncoding.DecodeString(secreto.IV)
	if err != nil {
		return "", err
	}
	tag, err := base64.StdEncoding.DecodeString(secreto.Tag)
	if err != nil {
		return "", err
	}
	bloque, err := aes.NewCipher(llave)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(bloque)
	if err != nil {
		return "", err
	}
	plano, err := gcm.Open(nil, iv, append(cifrado, tag...), nil)
	if err != nil {
		return "", errors.New("no se pudo descifrar el secreto")
	}
	return string(plano), nil
}
