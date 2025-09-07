package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

func main() {
	err := loadEnvFile(".env")
	if err != nil {
		panic(err)
	}
	http.HandleFunc("/incomingCall", func(w http.ResponseWriter, r *http.Request) {
		for key, values := range r.URL.Query() {
			log.Printf("Query param: %s = %v", key, values)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Failed to read body: %v", err)
			return
		}
		log.Printf("Body length: %d bytes", len(body))

		var jsonData any
		if err := json.Unmarshal(body, &jsonData); err != nil {
			log.Printf("Failed to decode JSON: %v", err)
		} else {
			log.Printf("JSON body: %v", jsonData)
		}

		w.Write([]byte("OK"))
	})

	log.Fatal(http.ListenAndServe("0.0.0.0:5000", nil))
}
