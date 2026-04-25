package fingerprint

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

type Fingerprint struct {
	Value   string
	Display string
}

func Compute(canonicalKey, secret string) Fingerprint {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonicalKey))
	sum := mac.Sum(nil)
	value := hex.EncodeToString(sum)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum)
	display := "tkfp_" + strings.ToLower(encoded[:12])
	return Fingerprint{Value: value, Display: display}
}
