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
// 内容: true が返る。
func TestVerifySignature_ValidSignature_ReturnsTrue(t *testing.T) {
	publicKey, privateKey := newKeyPair(t)
	signature := sign(privateKey, testTimestamp, testBody)

	got := ingress.VerifySignature(publicKey, signature, testTimestamp, []byte(testBody))

	if !got {
		t.Errorf("VerifySignature() = false, want true")
	}
}

// 条件: 署名した後に本文を変更した。
// 内容: false が返る。
func TestVerifySignature_TamperedBody_ReturnsFalse(t *testing.T) {
	publicKey, privateKey := newKeyPair(t)
	signature := sign(privateKey, testTimestamp, testBody)

	got := ingress.VerifySignature(publicKey, signature, testTimestamp, []byte(`{"type":2}`))

	if got {
		t.Errorf("VerifySignature() = true, want false")
	}
}

// 条件: 署名が 16 進でない "zz"。
// 内容: false が返る。
func TestVerifySignature_NonHexSignature_ReturnsFalse(t *testing.T) {
	publicKey, _ := newKeyPair(t)

	got := ingress.VerifySignature(publicKey, "zz", testTimestamp, []byte(testBody))

	if got {
		t.Errorf("VerifySignature() = true, want false")
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
