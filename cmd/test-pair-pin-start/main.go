package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: test-pair-pin-start <device-ip>")
		os.Exit(1)
	}

	deviceIP := os.Args[1]
	url := fmt.Sprintf("http://%s:7000/pair-pin-start", deviceIP)

	fmt.Printf("Testing /pair-pin-start against %s\n", url)

	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("User-Agent", "AirPlay/320.20")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Apple-HKP", "3")
	req.Header.Set("Content-Type", "application/octet-stream")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	fmt.Printf("Status: %d\n", resp.StatusCode)
	fmt.Printf("Headers: %v\n", resp.Header)
	fmt.Printf("Body length: %d bytes\n", len(body))
	if len(body) > 0 {
		fmt.Printf("Body (hex): %x\n", body)
		fmt.Printf("Body (text): %s\n", string(body))
	}
}

