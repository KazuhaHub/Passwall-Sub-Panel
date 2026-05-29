package mysql

import (
	"crypto/sha256"
	"fmt"
	"strings"

	pspcrypto "github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
)

const secretPrefix = "enc:v1:"

var dbSecretKey []byte

// ConfigureSecretKey installs the process-wide key material used by MySQL
// repositories to encrypt sensitive string fields before saving them.
func ConfigureSecretKey(material string) {
	material = strings.TrimSpace(material)
	if material == "" {
		dbSecretKey = nil
		return
	}
	sum := sha256.Sum256([]byte(material))
	key := make([]byte, len(sum))
	copy(key, sum[:])
	dbSecretKey = key
}

func encryptSecret(plaintext string) (string, error) {
	if plaintext == "" || strings.HasPrefix(plaintext, secretPrefix) {
		return plaintext, nil
	}
	if len(dbSecretKey) == 0 {
		return plaintext, nil
	}
	ciphertext, err := pspcrypto.EncryptString(dbSecretKey, plaintext)
	if err != nil {
		return "", err
	}
	return secretPrefix + ciphertext, nil
}

func decryptSecret(stored string) (string, error) {
	if stored == "" || !strings.HasPrefix(stored, secretPrefix) {
		return stored, nil
	}
	if len(dbSecretKey) == 0 {
		return "", fmt.Errorf("database secret is encrypted but no encryption key is configured")
	}
	plaintext, err := pspcrypto.DecryptString(dbSecretKey, strings.TrimPrefix(stored, secretPrefix))
	if err != nil {
		// A GCM auth failure here almost always means the encryption key
		// changed since this value was written. The most common cause is a
		// legacy config (no separate encryption_key) where jwt_secret doubles
		// as the at-rest key and the operator rotated jwt_secret — surface the
		// recovery path rather than a cryptic decrypt error that aborts boot.
		return "", fmt.Errorf("decrypt database secret: %w — the encryption key changed since this was stored; "+
			"if you rotated jwt_secret on a config without a separate encryption_key, restore the previous jwt_secret "+
			"(or set encryption_key to it) and restart", err)
	}
	return plaintext, nil
}
