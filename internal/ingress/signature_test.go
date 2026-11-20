package ingress_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"discord-ai-assistant/internal/ingress"
)

const (
	testTimestamp = "1700000000"
	testBody      = `{"type":1}`
)

func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return publicKey, privateKey
}

func sign(privateKey ed25519.PrivateKey, timestamp, body string) string {
	return hex.EncodeToString(ed25519.Sign(privateKey, []byte(timestamp+body)))
}

// 条件: timestamp + 本文に対する正しい署名。
// 内容: nil が返る。
func TestVerifySignature_ValidSignature_ReturnsNil(t *testing.T) {
	publicKey, privateKey := newKeyPair(t)
	signature := sign(privateKey, testTimestamp, testBody)

	got := ingress.VerifySignature(publicKey, signature, testTimestamp, []byte(testBody))

	if got != nil {
		t.Errorf("VerifySignature() = %v, want nil", got)
	}
}

// 条件: 署名が無い、16 進でない、長さが違う、署名した後に本文を変更した。
// 内容: それぞれ missing_header、invalid_hex、invalid_length、mismatch が返る。
func TestVerifySignature_InvalidSignature_ReturnsReason(t *testing.T) {
	publicKey, privateKey := newKeyPair(t)
	signature := sign(privateKey, testTimestamp, testBody)
	tests := []struct {
		name      string
		signature string
		body      string
		want      ingress.SignatureError
	}{
		{"missing header", "", testBody, "missing_header"},
		{"non hex", "zz", testBody, "invalid_hex"},
		{"short signature", "abcd", testBody, "invalid_length"},
		{"tampered body", signature, `{"type":2}`, "mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ingress.VerifySignature(publicKey, tt.signature, testTimestamp, []byte(tt.body))

			if got != tt.want {
				t.Errorf("VerifySignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

// 条件: 公開鍵が 16 進 4 文字 "abcd"。
// 内容: エラーが返る。
func TestParsePublicKey_WrongLength_ReturnsError(t *testing.T) {
	hexKey := "abcd"

	_, err := ingress.ParsePublicKey(hexKey)

	if err == nil {
		t.Errorf("ParsePublicKey() error = nil, want error")
	}
}
