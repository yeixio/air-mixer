package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: pair-verify-probe <device-ip>")
		os.Exit(1)
	}

	deviceIP := os.Args[1]
	fmt.Printf("Testing pair-verify requests against %s\n", deviceIP)
	fmt.Println("=" + string(make([]byte, 60)) + "=")

	// Encode TLV helper
	encodeTLV := func(tag byte, value []byte) []byte {
		length := len(value)
		if length > 255 {
			// TLV16
			result := make([]byte, 1+2+length)
			result[0] = tag
			result[1] = 0xFF
			result[2] = byte(length >> 8)
			result[3] = byte(length & 0xFF)
			copy(result[4:], value)
			return result
		}
		// TLV8
		result := make([]byte, 1+1+length)
		result[0] = tag
		result[1] = byte(length)
		copy(result[2:], value)
		return result
	}

	// Generate a temporary Ed25519 key pair for pair-verify
	_, clientPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Printf("ERROR: Failed to generate key: %v\n", err)
		os.Exit(1)
	}
	clientPublicKey := clientPrivateKey.Public().(ed25519.PublicKey)

	fmt.Printf("Generated client Ed25519 public key: %s\n", base64.StdEncoding.EncodeToString(clientPublicKey))

	// Get pairing IDs from /info
	infoURL := fmt.Sprintf("http://%s:7000/info", deviceIP)
	infoResp, err := http.Get(infoURL)
	var piStr, psiStr string
	if err == nil && infoResp.StatusCode == 200 {
		body, _ := io.ReadAll(infoResp.Body)
		infoResp.Body.Close()
		fmt.Printf("✓ Fetched /info (length: %d bytes)\n", len(body))
		piStr = "6203b015-90dd-400a-b0d0-6f26b762639f"  // From logs
		psiStr = "F2767822-FF61-47BF-B64A-9D7E10D21939" // From logs
	}

	// Test pair-verify M1
	// M1 for pair-verify contains:
	// - Tag 0x01 (State): 0x01 (M1)
	// - Tag 0x03 (Public Key): Client's Ed25519 public key (32 bytes)
	fmt.Println("\n=== Testing pair-verify ===")
	
	verifyM1 := append(
		encodeTLV(0x01, []byte{0x01}), // State = M1
		encodeTLV(0x03, clientPublicKey)..., // Public Key
	)

	fmt.Printf("pair-verify M1: %x (%d bytes)\n", verifyM1, len(verifyM1))

	verifyURL := fmt.Sprintf("http://%s:7000/pair-verify", deviceIP)
	req, err := http.NewRequest("POST", verifyURL, bytes.NewReader(verifyM1))
	if err != nil {
		fmt.Printf("ERROR: Failed to create request: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", "application/pairing+tlv8")
	req.Header.Set("User-Agent", "AirPlay/320.20")
	if piStr != "" {
		req.Header.Set("X-Apple-Pairing-ID", piStr)
	}
	if psiStr != "" {
		req.Header.Set("X-Apple-Pairing-State-ID", psiStr)
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("ERROR: Request failed: %v\n", err)
		os.Exit(1)
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	fmt.Printf("pair-verify M1 response status: %d\n", resp.StatusCode)
	if resp.StatusCode == 200 {
		fmt.Printf("✅ SUCCESS! Response body length: %d bytes\n", len(body))
		if len(body) > 0 {
			fmt.Printf("Response body (hex): %x\n", body[:min(100, len(body))])
		}
	} else {
		fmt.Printf("Response body: %s\n", string(body))
		fmt.Printf("Response headers: %v\n", resp.Header)
	}

	// Also test if pair-verify helps with pair-setup
	if resp.StatusCode == 200 {
		fmt.Println("\n=== Testing pair-setup after pair-verify ===")
		
		setupM1 := append(
			encodeTLV(0x01, []byte{0x00}), // Method = Pair Setup
			encodeTLV(0x06, []byte{0x01})..., // State = M1
		)

		setupURL := fmt.Sprintf("http://%s:7000/pair-setup", deviceIP)
		req2, _ := http.NewRequest("POST", setupURL, bytes.NewReader(setupM1))
		req2.Header.Set("Content-Type", "application/pairing+tlv8")
		req2.Header.Set("User-Agent", "AirPlay/320.20")
		if piStr != "" {
			req2.Header.Set("X-Apple-Pairing-ID", piStr)
		}
		if psiStr != "" {
			req2.Header.Set("X-Apple-Pairing-State-ID", psiStr)
		}

		resp2, _ := httpClient.Do(req2)
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()

		fmt.Printf("pair-setup M1 (after pair-verify) status: %d\n", resp2.StatusCode)
		if resp2.StatusCode == 200 {
			fmt.Printf("✅ SUCCESS! Response body length: %d bytes\n", len(body2))
		} else {
			fmt.Printf("Response body: %s\n", string(body2))
		}
	}

	fmt.Println("\n" + string(make([]byte, 60)) + "=")
	fmt.Println("Testing complete!")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

