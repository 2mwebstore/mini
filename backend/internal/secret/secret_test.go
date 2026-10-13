package secret

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSealOpen(t *testing.T) {
	b, _ := New("app-secret-at-least-32-characters-long")
	const token = "123456789:AAH-example_bot-token-value-1234567890"
	sealed, err := b.Seal(token)
	if err != nil || !strings.HasPrefix(sealed, prefix) || strings.Contains(sealed, "AAH-example") {
		t.Fatalf("sealed = %q, %v", sealed, err)
	}
	again, _ := b.Seal(token)
	if again == sealed {
		t.Fatal("each seal must use a fresh nonce")
	}
	if got, err := b.Open(sealed); err != nil || got != token {
		t.Fatalf("open = %q, %v", got, err)
	}
	if s, _ := b.Seal(""); s != "" {
		t.Fatal("empty stays empty")
	}
	if got, err := b.Open(""); got != "" || err != nil {
		t.Fatal("empty opens to empty")
	}

	other, _ := New("a-different-app-secret-of-32-chars-or-more")
	if _, err := other.Open(sealed); err != ErrUndecryptable {
		t.Fatalf("another key must not decrypt: %v", err)
	}
	if _, err := b.Open("plaintext-token"); err != ErrUndecryptable {
		t.Fatalf("unsealed values are rejected: %v", err)
	}
	// Flip one ciphertext byte (editing base64 text directly can decode to the same bytes).
	raw, _ := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sealed, prefix))
	raw[len(raw)/2] ^= 0x01
	if _, err := b.Open(prefix + base64.RawStdEncoding.EncodeToString(raw)); err != ErrUndecryptable {
		t.Fatalf("tampered values are rejected: %v", err)
	}
}
