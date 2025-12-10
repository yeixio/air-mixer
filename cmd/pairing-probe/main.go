package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: pairing-probe <device-ip>")
		fmt.Println("Example: pairing-probe 192.168.50.38")
		os.Exit(1)
	}

	deviceIP := os.Args[1]
	pairingURL := fmt.Sprintf("http://%s:7000/pair-setup", deviceIP)

	fmt.Printf("Probing pairing endpoint: %s\n\n", pairingURL)

	// Test cases to try
	testCases := []struct {
		name        string
		method      string
		contentType string
		body        []byte
		headers     map[string]string
	}{
		// Test 1: TLV8, Method only, POST, application/pairing+tlv8
		{
			name:        "TLV8 Method only, POST, pairing+tlv8",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x00}),
		},
		// Test 2: TLV8, Method only, PUT, application/pairing+tlv8
		{
			name:        "TLV8 Method only, PUT, pairing+tlv8",
			method:      "PUT",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x00}),
		},
		// Test 3: TLV8, State+Method, POST, application/pairing+tlv8
		{
			name:        "TLV8 State+Method, POST, pairing+tlv8",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        append(encodeTLV8(0x00, []byte{0x01}), encodeTLV8(0x01, []byte{0x00})...),
		},
		// Test 4: TLV8, Method only, POST, application/octet-stream
		{
			name:        "TLV8 Method only, POST, octet-stream",
			method:      "POST",
			contentType: "application/octet-stream",
			body:        encodeTLV8(0x01, []byte{0x00}),
		},
		// Test 5: TLV16, Method only, POST, application/pairing+tlv8
		{
			name:        "TLV16 Method only, POST, pairing+tlv8",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV16(0x01, []byte{0x00}),
		},
		// Test 6: TLV8, Method=1 (Pair Add), POST, pairing+tlv8
		{
			name:        "TLV8 Method=1, POST, pairing+tlv8",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x01}),
		},
		// Test 7: Empty body, POST, pairing+tlv8
		{
			name:        "Empty body, POST, pairing+tlv8",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        []byte{},
		},
		// Test 8: TLV8, Method only, POST, pairing+tlv8, with User-Agent
		{
			name:        "TLV8 Method only, POST, pairing+tlv8, with headers",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x00}),
			headers: map[string]string{
				"User-Agent": "AirPlay/320.20",
			},
		},
		// Test 9: TLV8, Method only, POST, pairing+tlv8, with all headers
		{
			name:        "TLV8 Method only, POST, pairing+tlv8, all headers",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x00}),
			headers: map[string]string{
				"User-Agent":              "AirPlay/320.20",
				"X-Apple-Bundle-ID":        "com.apple.TVRemote",
				"X-Apple-Client-Version":    "320.20",
			},
		},
		// Test 10: Try different endpoint - pair-verify
		{
			name:        "TLV8 Method only, POST, pairing+tlv8, pair-verify endpoint",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x00}),
		},
		// Test 11: Try with pairing+tlv8 Content-Type (revert back)
		{
			name:        "TLV8 Method only, POST, pairing+tlv8 (revert)",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x00}),
		},
		// Test 11: Try different port - 5556 (HomeKit standard)
		{
			name:        "TLV8 Method only, POST, pairing+tlv8, port 5556",
			method:      "POST",
			contentType: "application/pairing+tlv8",
			body:        encodeTLV8(0x01, []byte{0x00}),
		},
		// Test 12: Try GET request to pair-setup (maybe it's info endpoint)
		{
			name:        "GET request to pair-setup",
			method:      "GET",
			contentType: "application/pairing+tlv8",
			body:        []byte{},
		},
		// Test 13: Try OPTIONS request
		{
			name:        "OPTIONS request to pair-setup",
			method:      "OPTIONS",
			contentType: "application/pairing+tlv8",
			body:        []byte{},
		},
		// Test 14: Try /info endpoint
		{
			name:        "GET request to /info",
			method:      "GET",
			contentType: "application/pairing+tlv8",
			body:        []byte{},
		},
		// Test 15: Try /server-info endpoint
		{
			name:        "GET request to /server-info",
			method:      "GET",
			contentType: "application/pairing+tlv8",
			body:        []byte{},
		},
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}

	for i, test := range testCases {
		fmt.Printf("Test %d: %s\n", i+1, test.name)
		fmt.Printf("  Method: %s\n", test.method)
		fmt.Printf("  Content-Type: %s\n", test.contentType)
		fmt.Printf("  Body: %x (%d bytes)\n", test.body, len(test.body))

		url := pairingURL
		// Test 10 uses different endpoint
		if i == 9 {
			url = fmt.Sprintf("http://%s:7000/pair-verify", deviceIP)
		} else if i == 10 {
			// Test 11: Different port
			url = fmt.Sprintf("http://%s:5556/pair-setup", deviceIP)
		} else if i == 11 || i == 12 {
			// Tests 12-13: GET/OPTIONS to pair-setup
			url = pairingURL
		} else if i == 13 {
			// Test 14: /info endpoint
			url = fmt.Sprintf("http://%s:7000/info", deviceIP)
		} else if i == 14 {
			// Test 15: /server-info endpoint
			url = fmt.Sprintf("http://%s:7000/server-info", deviceIP)
		}

		req, err := http.NewRequest(test.method, url, bytes.NewReader(test.body))
		if err != nil {
			fmt.Printf("  ERROR: Failed to create request: %v\n\n", err)
			continue
		}

		req.Header.Set("Content-Type", test.contentType)
		for k, v := range test.headers {
			req.Header.Set(k, v)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			fmt.Printf("  ERROR: Request failed: %v\n\n", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		fmt.Printf("  Response: %d %s\n", resp.StatusCode, resp.Status)
		fmt.Printf("  Response Headers: %v\n", resp.Header)
		if len(body) > 0 {
			fmt.Printf("  Response Body: %x (%d bytes)\n", body, len(body))
			if len(body) < 100 {
				fmt.Printf("  Response Body (text): %s\n", string(body))
			}
		}

		// Highlight successful responses (200 OK)
		if resp.StatusCode == 200 {
			fmt.Printf("  *** SUCCESS! This combination works! ***\n")
		} else if resp.StatusCode != 470 && resp.StatusCode != 403 {
			fmt.Printf("  *** Interesting status code (not 403/470) ***\n")
		}

		fmt.Println()
		time.Sleep(500 * time.Millisecond) // Small delay between requests
	}
}

// encodeTLV8 encodes TLV with 1-byte length (TLV8)
func encodeTLV8(tag byte, value []byte) []byte {
	length := len(value)
	if length > 255 {
		panic("TLV8 cannot encode values > 255 bytes")
	}
	result := make([]byte, 1+1+length)
	result[0] = tag
	result[1] = byte(length)
	copy(result[2:], value)
	return result
}

// encodeTLV16 encodes TLV with 2-byte length (TLV16)
func encodeTLV16(tag byte, value []byte) []byte {
	length := len(value)
	result := make([]byte, 1+2+length)
	result[0] = tag
	binary.BigEndian.PutUint16(result[1:3], uint16(length))
	copy(result[3:], value)
	return result
}

