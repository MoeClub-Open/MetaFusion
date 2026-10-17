package assistant

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// ParseEncryptionKey accepts exactly a base64 encoded AES-256 master key.
func ParseEncryptionKey(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("invalid_ai_encryption_key")
	}
	return key, nil
}

func (s *Store) encryptSecret(secret, identity string) ([]byte, error) {
	if len(s.EncryptionKey) != 32 {
		return nil, ErrEncryptionUnavailable
	}
	block, err := aes.NewCipher(s.EncryptionKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(secret), []byte(identity)), nil
}

func (s *Store) decryptSecret(encrypted []byte, identity string) (string, error) {
	if len(s.EncryptionKey) != 32 {
		return "", ErrEncryptionUnavailable
	}
	block, err := aes.NewCipher(s.EncryptionKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(encrypted) < aead.NonceSize() {
		return "", fmt.Errorf("assistant_invalid_ciphertext")
	}
	plain, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(identity))
	if err != nil {
		return "", fmt.Errorf("assistant_credential_decryption_failed")
	}
	return string(plain), nil
}

func keyHint(secret string) string {
	if len(secret) < 8 {
		return "••••"
	}
	return "••••" + secret[len(secret)-4:]
}
