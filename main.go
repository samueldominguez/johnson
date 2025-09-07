package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type TwilioMedia struct {
	Event     string `json:"event"`
	StreamSid string `json:"streamSid"`
	Media     struct {
		Payload string `json:"payload"`
	} `json:"media"`
}

func main() {
	err := loadEnvFile(".env")
	if err != nil {
		panic(err)
	}
	// Read and encode WAV file
	wavData, err := os.ReadFile("audio.wav")
	if err != nil {
		log.Fatal("Error reading WAV:", err)
	}
	// Skip WAV header (44 bytes for standard WAV)
	pcmData := wavData[44:]
	base64Audio := base64.StdEncoding.EncodeToString(pcmData)

	// WebSocket handler
	http.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		log.Println("stream started")
		log.Println("headers:", r.Header)
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("WebSocket upgrade error:", err)
			return
		}
		defer ws.Close()

		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				log.Println("WebSocket read error:", err)
				return
			}

			log.Println(string(msg))
			// Parse incoming Twilio message
			var media TwilioMedia
			if err := json.Unmarshal(msg, &media); err != nil {
				log.Println("JSON parse error:", err)
				continue
			}

			// Respond to media event with pre-recorded audio
			if media.Event == "media" {
				response := TwilioMedia{
					Event:     "media",
					StreamSid: media.StreamSid,
					Media: struct {
						Payload string `json:"payload"`
					}{Payload: base64Audio},
				}
				responseJSON, err := json.Marshal(response)
				if err != nil {
					log.Println("JSON marshal error:", err)
					continue
				}
				if err := ws.WriteMessage(websocket.TextMessage, responseJSON); err != nil {
					log.Println("WebSocket write error:", err)
					return
				}
			}
		}
	})

	http.HandleFunc("/streamStatus", func(w http.ResponseWriter, r *http.Request) {
		log.Println("received stream status")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println("error reading body", err)
			return
		}
		s := string(body)
		log.Println("SS:", s)
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/incomingCall", func(w http.ResponseWriter, r *http.Request) {
		log.Println("received call")
		twiml := `<?xml version="1.0" encoding="UTF-8"?>
		<Response>
			<Connect>
				<Stream url="wss://unminimizer.com/stream" statusCallback="https://unminimizer.com/streamStatus" />
			</Connect>
			<Say>The stream has started.</Say>
		</Response>`
		w.Header().Set("Content-Type", "text/xml")
		_, err := fmt.Fprint(w, twiml)
		if err != nil {
			log.Println("error writing twiml response", err)
		}
	})

	log.Fatal(http.ListenAndServe("0.0.0.0:5000", nil))
}
