package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: m1-probe <device-ip>")
		os.Exit(1)
	}

	deviceIP := os.Args[1]
	fmt.Printf("Testing M1 pairing requests against %s\n", deviceIP)
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

	// Test cases
	type testCase struct {
		name        string
		method      string
		url         string
		contentType string
		body        []byte
		headers     map[string]string
	}

	// Get pairing IDs from /info first
	infoURL := fmt.Sprintf("http://%s:7000/info", deviceIP)
	infoResp, err := http.Get(infoURL)
	var piStr, psiStr string
	if err == nil && infoResp.StatusCode == 200 {
		body, _ := io.ReadAll(infoResp.Body)
		infoResp.Body.Close()
		fmt.Printf("✓ Fetched /info (length: %d bytes)\n", len(body))
		// Try to extract pi and psi (simplified - would need proper plist parsing)
		// For now, we'll use placeholders
		piStr = "6203b015-90dd-400a-b0d0-6f26b762639f"  // From logs
		psiStr = "F2767822-FF61-47BF-B64A-9D7E10D21939" // From logs
	}

	testCases := []testCase{
		// Test 1: Method only (original)
		{
			name:        "Method only, RTSP POST, pairing+tlv8",
			method:      "POST",
			url:         fmt.Sprintf("rtsp://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        encodeTLV(0x01, []byte{0x00}),
		},
		// Test 2: Method + State (current)
		{
			name:        "Method + State, RTSP POST, pairing+tlv8",
			method:      "POST",
			url:         fmt.Sprintf("rtsp://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        append(encodeTLV(0x01, []byte{0x00}), encodeTLV(0x06, []byte{0x01})...),
		},
		// Test 3: Method + State with pairing IDs
		{
			name:        "Method + State + Pairing IDs, RTSP POST",
			method:      "POST",
			url:         fmt.Sprintf("rtsp://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        append(encodeTLV(0x01, []byte{0x00}), encodeTLV(0x06, []byte{0x01})...),
			headers: map[string]string{
				"X-Apple-Pairing-ID":      piStr,
				"X-Apple-Pairing-State-ID": psiStr,
			},
		},
		// Test 4: Method only, HTTP POST
		{
			name:        "Method only, HTTP POST, pairing+tlv8",
			method:      "POST",
			url:         fmt.Sprintf("http://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        encodeTLV(0x01, []byte{0x00}),
		},
		// Test 5: Method + State, HTTP POST
		{
			name:        "Method + State, HTTP POST, pairing+tlv8",
			method:      "POST",
			url:         fmt.Sprintf("http://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        append(encodeTLV(0x01, []byte{0x00}), encodeTLV(0x06, []byte{0x01})...),
		},
		// Test 6: Method + State + Pairing IDs, HTTP POST
		{
			name:        "Method + State + Pairing IDs, HTTP POST",
			method:      "POST",
			url:         fmt.Sprintf("http://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        append(encodeTLV(0x01, []byte{0x00}), encodeTLV(0x06, []byte{0x01})...),
			headers: map[string]string{
				"X-Apple-Pairing-ID":      piStr,
				"X-Apple-Pairing-State-ID": psiStr,
			},
		},
		// Test 7: Method only, binary-plist Content-Type
		{
			name:        "Method only, HTTP POST, binary-plist",
			method:      "POST",
			url:         fmt.Sprintf("http://%s:7000/pair-setup", deviceIP),
			contentType: "application/x-apple-binary-plist",
			body:        encodeTLV(0x01, []byte{0x00}),
		},
		// Test 8: Method + State, binary-plist
		{
			name:        "Method + State, HTTP POST, binary-plist",
			method:      "POST",
			url:         fmt.Sprintf("http://%s:7000/pair-setup", deviceIP),
			contentType: "application/x-apple-binary-plist",
			body:        append(encodeTLV(0x01, []byte{0x00}), encodeTLV(0x06, []byte{0x01})...),
		},
		// Test 9: State only (unusual but worth trying)
		{
			name:        "State only, HTTP POST",
			method:      "POST",
			url:         fmt.Sprintf("http://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        encodeTLV(0x06, []byte{0x01}),
		},
		// Test 10: Method + State, different User-Agent
		{
			name:        "Method + State, AirPlay User-Agent",
			method:      "POST",
			url:         fmt.Sprintf("http://%s:7000/pair-setup", deviceIP),
			contentType: "application/pairing+tlv8",
			body:        append(encodeTLV(0x01, []byte{0x00}), encodeTLV(0x06, []byte{0x01})...),
			headers: map[string]string{
				"User-Agent": "AirPlay/320.20",
			},
		},
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}

	for i, test := range testCases {
		fmt.Printf("\nTest %d: %s\n", i+1, test.name)
		fmt.Printf("  Method: %s\n", test.method)
		fmt.Printf("  URL: %s\n", test.url)
		fmt.Printf("  Content-Type: %s\n", test.contentType)
		fmt.Printf("  Body: %x (%d bytes)\n", test.body, len(test.body))

		// Handle RTSP vs HTTP
		var resp *http.Response
		var err error

		if test.url[:4] == "rtsp" {
			// RTSP POST - would need custom RTSP client
			fmt.Printf("  ⚠ Skipping RTSP (requires custom RTSP client)\n")
			continue
		}

		req, err := http.NewRequest(test.method, test.url, bytes.NewReader(test.body))
		if err != nil {
			fmt.Printf("  ✗ ERROR: Failed to create request: %v\n", err)
			continue
		}

		req.Header.Set("Content-Type", test.contentType)
		if test.headers != nil {
			for k, v := range test.headers {
				req.Header.Set(k, v)
			}
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "AirPlay/320.20")
		}

		resp, err = httpClient.Do(req)
		if err != nil {
			fmt.Printf("  ✗ ERROR: Request failed: %v\n", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		status := resp.StatusCode
		statusEmoji := "✓"
		if status == 200 {
			statusEmoji = "✅"
			fmt.Printf("  %s SUCCESS! Status: %d\n", statusEmoji, status)
			fmt.Printf("  Response body length: %d bytes\n", len(body))
			if len(body) > 0 {
				fmt.Printf("  Response body (hex): %x\n", body[:min(50, len(body))])
			}
		} else if status != 470 && status != 403 {
			fmt.Printf("  %s Status: %d (not 470/403!)\n", statusEmoji, status)
			fmt.Printf("  Response body: %s\n", string(body))
		} else {
			fmt.Printf("  %s Status: %d\n", statusEmoji, status)
		}

		// Show headers
		if len(resp.Header) > 0 {
			fmt.Printf("  Response headers: %v\n", resp.Header)
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

