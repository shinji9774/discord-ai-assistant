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

// SignatureError は署名の検証に失敗した理由。値は snake_case の固定文字列で、ログにそのまま出せる。
type SignatureError string

const (
	SignatureMissingHeader SignatureError = "missing_header" // 署名のヘッダが無い
	SignatureInvalidHex    SignatureError = "invalid_hex"    // 署名が 16 進でない
	SignatureInvalidLength SignatureError = "invalid_length" // 署名の長さが Ed25519 の署名の長さでない
	SignatureMismatch      SignatureError = "mismatch"       // 署名が timestamp + 本文と合わない
)

func (e SignatureError) Error() string { return string(e) }

// VerifySignature は timestamp + body に対する署名（16 進）を検証する。成功なら nil、失敗なら SignatureError を返す。
func VerifySignature(publicKey ed25519.PublicKey, signatureHex, timestamp string, body []byte) error {
	if signatureHex == "" {
		return SignatureMissingHeader
	}
	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return SignatureInvalidHex
	}
	if len(signature) != ed25519.SignatureSize {
		return SignatureInvalidLength
	}
	message := append([]byte(timestamp), body...)
	if !ed25519.Verify(publicKey, message, signature) {
		return SignatureMismatch
	}
	return nil
}
