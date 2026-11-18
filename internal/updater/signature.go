package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
)

// PublicKey is the Ed25519 key releases are signed with. It is injected at
// build time so an attacker who can reach the download cannot swap the key:
//
//	-ldflags "-X 'obblog/internal/updater.PublicKey=<base64>"
//
// An empty value disables installation entirely while leaving version checks
// working, which is the safe default for a build that has no key configured.
var PublicKey = ""

// parsePublicKey accepts the PEM block openssl produces, a bare base64 body, or
// the base64 that the settings page shows.
func parsePublicKey(encoded string) (ed25519.PublicKey, error) {
	raw := strings.TrimSpace(encoded)
	if raw == "" {
		return nil, errors.New("未配置签名公钥，无法验证更新包")
	}
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		raw = string(block.Bytes)
		return finishPublicKey(raw)
	}
	// A PEM body pasted without its header is common; accept it too.
	raw = strings.Join(strings.Fields(raw), "")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("签名公钥格式无效")
	}
	return finishPublicKey(string(decoded))
}

func finishPublicKey(data string) (ed25519.PublicKey, error) {
	bytes := []byte(data)
	// A DER SubjectPublicKeyInfo for Ed25519 is a fixed 12-byte header plus the
	// 32-byte key. Accept it so openssl output can be pasted directly.
	const derPrefixLen = 12
	if len(bytes) == derPrefixLen+ed25519.PublicKeySize && bytes[0] == 0x30 {
		bytes = bytes[derPrefixLen:]
	}
	if len(bytes) != ed25519.PublicKeySize {
		return nil, errors.New("签名公钥长度无效")
	}
	return ed25519.PublicKey(bytes), nil
}

// Verify reports whether signature is a valid signature of message under key.
func Verify(key ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(key, message, signature)
}
