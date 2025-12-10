package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"howett.net/plist"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: parse-info <device-ip>")
		fmt.Println("Example: parse-info 192.168.50.38")
		os.Exit(1)
	}

	deviceIP := os.Args[1]
	infoURL := fmt.Sprintf("http://%s:7000/info", deviceIP)

	fmt.Printf("Fetching device info from: %s\n\n", infoURL)

	resp, err := http.Get(infoURL)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Error: Got status %d\n", resp.StatusCode)
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Response length: %d bytes\n", len(body))
	fmt.Printf("Response (first 200 bytes as text): %s\n\n", string(body[:min(200, len(body))]))

	// Check if it's binary plist
	if len(body) >= 8 && string(body[:8]) == "bplist00" {
		fmt.Println("Detected binary plist format")
		fmt.Println("\nParsing plist...")
		
		var plistData interface{}
		_, err := plist.Unmarshal(body, &plistData)
		if err != nil {
			fmt.Printf("Error parsing plist: %v\n", err)
			fmt.Println("\nTrying to find readable strings...")
			findReadableStrings(body)
			return
		}
		
		// Convert to JSON for readable output
		jsonData, err := json.MarshalIndent(plistData, "", "  ")
		if err != nil {
			fmt.Printf("Error converting to JSON: %v\n", err)
			fmt.Printf("Raw plist data type: %T\n", plistData)
			return
		}
		
		fmt.Println("\nParsed device information:")
		fmt.Println(string(jsonData))
		
		// Extract key fields if it's a map
		if m, ok := plistData.(map[string]interface{}); ok {
			fmt.Println("\n=== Key Device Information ===")
			if name, ok := m["name"].(string); ok {
				fmt.Printf("Device Name: %s\n", name)
			}
			if deviceID, ok := m["deviceID"].(string); ok {
				fmt.Printf("Device ID: %s\n", deviceID)
			}
			if model, ok := m["model"].(string); ok {
				fmt.Printf("Model: %s\n", model)
			}
			if version, ok := m["sourceVersion"].(string); ok {
				fmt.Printf("Source Version: %s\n", version)
			}
			if protocol, ok := m["protocolVersion"].(string); ok {
				fmt.Printf("Protocol Version: %s\n", protocol)
			}
			if features, ok := m["features"].(uint64); ok {
				fmt.Printf("Features (bitfield): %d (0x%x)\n", features, features)
			}
			if mac, ok := m["macAddress"].(string); ok {
				fmt.Printf("MAC Address: %s\n", mac)
			}
			if pk, ok := m["pk"].(string); ok {
				fmt.Printf("Public Key: %s\n", pk)
			}
		}
	} else {
		fmt.Println("Not a standard binary plist format")
	}
}

func findReadableStrings(data []byte) {
	inString := false
	currentString := []byte{}
	
	for i := 0; i < len(data); i++ {
		b := data[i]
		
		// Look for ASCII printable characters
		if b >= 32 && b <= 126 {
			if !inString {
				inString = true
				currentString = []byte{}
			}
			currentString = append(currentString, b)
		} else {
			if inString && len(currentString) > 3 {
				// Found a string of at least 4 characters
				fmt.Printf("  Found string: %s\n", string(currentString))
			}
			inString = false
			currentString = []byte{}
		}
	}
	
	if inString && len(currentString) > 3 {
		fmt.Printf("  Found string: %s\n", string(currentString))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

