// Package secret encrypts credentials stored in the database (the Telegram bot
// token), so a leaked database backup does not leak them. AES-256-GCM with a key
// derived from JWT_SECRET: no extra environment variable to manage. Rotating
// JWT_SECRET makes stored secrets unreadable; they are then treated as unset and
// must be entered again in Settings.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

const prefix = "enc:v1:"

var ErrUndecryptable = errors.New("secret: cannot decrypt (key changed or data corrupted)")

type Box struct{ aead cipher.AEAD }

// New derives a dedicated encryption key from the application secret.
func New(appSecret string) (*Box, error) {
	m := hmac.New(sha256.New, []byte(appSecret))
	m.Write([]byte("attendance/settings-encryption/v1"))
	block, err := aes.NewCipher(m.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; the empty string stays empty (= not set).
func (b *Box) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if !strings.HasPrefix(stored, prefix) || err != nil || len(raw) < b.aead.NonceSize() {
		return "", ErrUndecryptable
	}
	n := b.aead.NonceSize()
	pt, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", ErrUndecryptable
	}
	return string(pt), nil
}
