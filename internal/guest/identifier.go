package guest

import (
	"crypto/rand"
	"encoding/hex"
)

func newIdentifier() string {
	identifierBytes := make([]byte, 16)
	_, errorValue := rand.Read(identifierBytes)
	if errorValue != nil {
		return "0000000000000000"
	}

	return hex.EncodeToString(identifierBytes)
}
