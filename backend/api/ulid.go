package api

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// crockford32 is Crockford's Base32 alphabet in lowercase for URL safety and greppability.
const crockford32 = "0123456789abcdefghjkmnpqrstvwxyz"

var (
	ulidMu       sync.Mutex
	lastULIDTime int64
	lastEntropy  [10]byte
)

// generateULID generates a 26-character Universally Unique Lexicographically Sortable Identifier.
// It consists of a 48-bit Unix millisecond timestamp (10 chars) followed by 80 bits of
// cryptographic entropy (16 chars), guaranteeing chronological sortability and collision safety.
func generateULID(now time.Time) (string, error) {
	ms := now.UnixMilli()

	ulidMu.Lock()
	defer ulidMu.Unlock()

	var entropy [10]byte
	if ms == lastULIDTime {
		// Monotonic increment of entropy within the same millisecond
		entropy = lastEntropy
		for i := 9; i >= 0; i-- {
			entropy[i]++
			if entropy[i] != 0 {
				break
			}
		}
	} else {
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", fmt.Errorf("generate ulid entropy: %w", err)
		}
		lastULIDTime = ms
	}
	lastEntropy = entropy

	var dst [26]byte

	// 10 characters of timestamp (48 bits = 50 bits encoded, top 2 bits 0)
	dst[0] = crockford32[(ms>>45)&0x1f]
	dst[1] = crockford32[(ms>>40)&0x1f]
	dst[2] = crockford32[(ms>>35)&0x1f]
	dst[3] = crockford32[(ms>>30)&0x1f]
	dst[4] = crockford32[(ms>>25)&0x1f]
	dst[5] = crockford32[(ms>>20)&0x1f]
	dst[6] = crockford32[(ms>>15)&0x1f]
	dst[7] = crockford32[(ms>>10)&0x1f]
	dst[8] = crockford32[(ms>>5)&0x1f]
	dst[9] = crockford32[ms&0x1f]

	// 16 characters of entropy (80 bits)
	// Block 1: entropy[0..4] (40 bits -> 8 chars)
	dst[10] = crockford32[(entropy[0]>>3)&0x1f]
	dst[11] = crockford32[((entropy[0]<<2)|(entropy[1]>>6))&0x1f]
	dst[12] = crockford32[(entropy[1]>>1)&0x1f]
	dst[13] = crockford32[((entropy[1]<<4)|(entropy[2]>>4))&0x1f]
	dst[14] = crockford32[((entropy[2]<<1)|(entropy[3]>>7))&0x1f]
	dst[15] = crockford32[(entropy[3]>>2)&0x1f]
	dst[16] = crockford32[((entropy[3]<<3)|(entropy[4]>>5))&0x1f]
	dst[17] = crockford32[entropy[4]&0x1f]

	// Block 2: entropy[5..9] (40 bits -> 8 chars)
	dst[18] = crockford32[(entropy[5]>>3)&0x1f]
	dst[19] = crockford32[((entropy[5]<<2)|(entropy[6]>>6))&0x1f]
	dst[20] = crockford32[(entropy[6]>>1)&0x1f]
	dst[21] = crockford32[((entropy[6]<<4)|(entropy[7]>>4))&0x1f]
	dst[22] = crockford32[((entropy[7]<<1)|(entropy[8]>>7))&0x1f]
	dst[23] = crockford32[(entropy[8]>>2)&0x1f]
	dst[24] = crockford32[((entropy[8]<<3)|(entropy[9]>>5))&0x1f]
	dst[25] = crockford32[entropy[9]&0x1f]

	return string(dst[:]), nil
}
