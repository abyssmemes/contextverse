package spacesvc

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

func decodeStdB64(s string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// try raw std without padding issues
		data, err2 := base64.RawStdEncoding.DecodeString(s)
		if err2 != nil {
			return nil, fmt.Errorf("base64: %w", err)
		}
		return data, nil
	}
	return data, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
