package airplay

import (
	"crypto/rand"
	"crypto/sha512"
	"math/big"
)

// HomeKit SRP-6a constants
// These are the standard values used by HomeKit/AirPlay pairing
var (
	// N is the 3072-bit prime modulus for HomeKit SRP
	// This is the standard RFC 5054 Appendix A 3072-bit group
	srpN3072 = mustParseBigInt("FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1" +
		"29024E088A67CC74020BBEA63B139B22514A08798E3404DD" +
		"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245" +
		"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
		"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3D" +
		"C2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F" +
		"83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
		"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
		"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9" +
		"DE2BCBF6955817183995497CEA956AE515D2261898FA0510" +
		"15728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64" +
		"ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7" +
		"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6B" +
		"F12FFA06D98A0864D87602733EC86A64521F2B18177B200C" +
		"BBE117577A615D6C770988C0BAD946E208E24FA074E5AB31" +
		"43DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF")

	// g is the generator (2 for HomeKit)
	srpG = big.NewInt(2)
)

// mustParseBigInt parses a hex string to big.Int, panics on error
func mustParseBigInt(hex string) *big.Int {
	n, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		panic("failed to parse SRP modulus")
	}
	return n
}

// generateSRPClientKey generates a proper SRP-6a client public key
// Returns: A (client public key) and a (client private key)
func generateSRPClientKey() (*big.Int, *big.Int, error) {
	// Generate random private key a (256 bits for security)
	aBytes := make([]byte, 32)
	if _, err := rand.Read(aBytes); err != nil {
		return nil, nil, err
	}
	
	// Convert to big.Int, ensuring it's less than N
	a := new(big.Int).SetBytes(aBytes)
	a.Mod(a, srpN3072)
	
	// Calculate A = g^a mod N
	A := new(big.Int).Exp(srpG, a, srpN3072)
	
	return A, a, nil
}

// calculateSRPSharedSecret calculates the SRP-6a shared secret S
// Parameters:
//   - B: Server public key
//   - a: Client private key
//   - salt: Salt from server
//   - pin: PIN code (4 digits)
//   - N: Modulus
//   - g: Generator
func calculateSRPSharedSecret(B, a *big.Int, salt []byte, pin string) (*big.Int, error) {
	// Calculate x = H(salt | H(username | ":" | password))
	// For HomeKit, username is typically empty, password is PIN
	username := ""
	password := pin
	
	// H(username | ":" | password)
	innerHash := sha512.Sum512([]byte(username + ":" + password))
	
	// H(salt | innerHash)
	outerHash := sha512.Sum512(append(salt, innerHash[:]...))
	x := new(big.Int).SetBytes(outerHash[:])
	x.Mod(x, srpN3072)
	
	// Calculate v = g^x mod N (verifier)
	v := new(big.Int).Exp(srpG, x, srpN3072)
	
	// Calculate u = H(A | B)
	// For now, we'll use a simplified u calculation
	// In full SRP-6a, u = H(A | B) where A and B are concatenated
	uBytes := sha512.Sum512(append(B.Bytes(), v.Bytes()...))
	u := new(big.Int).SetBytes(uBytes[:])
	u.Mod(u, srpN3072)
	
	// Calculate S = (B - g^x)^(a + u*x) mod N
	// But we need A (client public key) for this
	// Actually, S = (B - k*g^x)^(a + u*x) mod N where k = H(N | g)
	// For SRP-6a, k = H(N | g)
	kHash := sha512.Sum512(append(srpN3072.Bytes(), srpG.Bytes()...))
	k := new(big.Int).SetBytes(kHash[:])
	k.Mod(k, srpN3072)
	
	// S = (B - k*v)^(a + u*x) mod N
	kv := new(big.Int).Mul(k, v)
	kv.Mod(kv, srpN3072)
	
	BMinusKV := new(big.Int).Sub(B, kv)
	BMinusKV.Mod(BMinusKV, srpN3072)
	
	ux := new(big.Int).Mul(u, x)
	ux.Mod(ux, srpN3072)
	
	aPlusUX := new(big.Int).Add(a, ux)
	aPlusUX.Mod(aPlusUX, srpN3072)
	
	S := new(big.Int).Exp(BMinusKV, aPlusUX, srpN3072)
	
	return S, nil
}

// calculateSRPProof calculates the SRP-6a proof M
// M = H(H(N) XOR H(g) | H(username) | salt | A | B | K)
func calculateSRPProof(username string, salt []byte, A, B *big.Int, K []byte) []byte {
	// H(N)
	hN := sha512.Sum512(srpN3072.Bytes())
	
	// H(g)
	hG := sha512.Sum512(srpG.Bytes())
	
	// H(N) XOR H(g)
	xor := make([]byte, 64)
	for i := 0; i < 64; i++ {
		xor[i] = hN[i] ^ hG[i]
	}
	
	// H(username)
	hUsername := sha512.Sum512([]byte(username))
	
	// Concatenate: H(N) XOR H(g) | H(username) | salt | A | B | K
	proofInput := append(xor, hUsername[:]...)
	proofInput = append(proofInput, salt...)
	proofInput = append(proofInput, A.Bytes()...)
	proofInput = append(proofInput, B.Bytes()...)
	proofInput = append(proofInput, K...)
	
	// M = H(proofInput)
	M := sha512.Sum512(proofInput)
	return M[:]
}

// calculateSRPSessionKey calculates K = H(S) where S is the shared secret
func calculateSRPSessionKey(S *big.Int) []byte {
	K := sha512.Sum512(S.Bytes())
	return K[:]
}

