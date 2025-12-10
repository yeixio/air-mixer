package webui

import (
	"embed"
	"fmt"
	"log"
	"net/http"
)

//go:embed index.html
var webUIFiles embed.FS

func rootHandler(w http.ResponseWriter, r *http.Request) {
	// Don't serve UI for API routes
	if r.URL.Path == "/api" || len(r.URL.Path) > 4 && r.URL.Path[:5] == "/api/" {
		http.NotFound(w, r)
		return
	}
	
	// Serve embedded web UI
	content, err := webUIFiles.ReadFile("index.html")
	if err != nil {
		http.Error(w, "Failed to load web UI", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(content)
}

// RootHandler is the exported handler for use with gorilla/mux
func RootHandler(w http.ResponseWriter, r *http.Request) {
	rootHandler(w, r)
}

// LaunchWebUI starts the web UI server
// This should be integrated with the main API server, not run separately
func LaunchWebUI(port int) {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("INFO: Air-Mixer Web UI started: http://localhost%s", addr)
	
	http.HandleFunc("/", rootHandler)
	
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}
}

