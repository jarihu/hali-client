package daemon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

func Sign(secret []byte, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func Verify(secret []byte, payload []byte, sig string) bool {
	decoded, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)

	return hmac.Equal(mac.Sum(nil), decoded)
}
