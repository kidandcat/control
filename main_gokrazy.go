//go:build gokrazy

package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	log.Println("Control service starting...")
	
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from Control service! Time: %s\n", time.Now().Format(time.RFC3339))
	})
	
	log.Println("Starting HTTP server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}