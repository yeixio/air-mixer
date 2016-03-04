package webui

import (
	"io"
	"log"
	"net/http"
)

func rootHandler(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "hello, world\n")
}

func LaunchWebUI() {
	log.Println("INFO: AirServer Started: http://localhost:9001")
	http.HandleFunc("/", rootHandler)
	http.ListenAndServe(":9001", nil)
}
