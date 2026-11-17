package ingress

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
)

// Discord の公開鍵（Ed25519）の 16 進表記の長さ。
const publicKeyHexLength = ed25519.PublicKeySize * 2

// ParsePublicKey は Discord の公開鍵（16 進 64 文字）を ed25519.PublicKey にする。
func ParsePublicKey(hexKey string) (ed25519.PublicKey, error) {
	if len(hexKey) != publicKeyHexLength {
		return nil, fmt.Errorf("public key must be %d hex characters", publicKeyHexLength)
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("public key is not hex: %w", err)
	}
	return ed25519.PublicKey(key), nil
}

// VerifySignature は timestamp + body に対する署名（16 進）を検証する。
func VerifySignature(publicKey ed25519.PublicKey, signatureHex, timestamp string, body []byte) bool {
	signature, err := hex.DecodeString(signatureHex)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false
	}
	message := append([]byte(timestamp), body...)
	return ed25519.Verify(publicKey, message, signature)
}
