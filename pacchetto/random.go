package pacchetto

import (
	"encoding/binary"
	"math/rand/v2"
)

func RandomFunction(seed uint64, probability float64) bool {
	var seedBytes [32]byte
	binary.LittleEndian.PutUint64(seedBytes[0:8], seed)
	rand := rand.New(rand.NewChaCha8(seedBytes))

	result := rand.Float64()
	return result < probability
}
