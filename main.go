package main

import (
	"encoding/base64"
	"encoding/binary"
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
		Track   string `json:"track"`
		Payload string `json:"payload"`
	} `json:"media"`
}

func createWavHeader(dataLength int) []byte {
	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataLength)) // File size - 8
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16)   // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 7)    // μ-law format
	binary.LittleEndian.PutUint16(header[22:24], 1)    // Mono
	binary.LittleEndian.PutUint32(header[24:28], 8000) // Sample rate
	binary.LittleEndian.PutUint32(header[28:32], 8000) // Byte rate
	binary.LittleEndian.PutUint16(header[32:34], 1)    // Block align
	binary.LittleEndian.PutUint16(header[34:36], 8)    // Bits per sample
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataLength)) // Data size
	return header
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
		var audioBuffer []byte // Accumulate μ-law data for this call
		var audioFile *os.File // File for this call
		var streamSid string   // Unique identifier for the call
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
				// Write WAV header and data before closing
				if len(audioBuffer) > 0 && audioFile != nil {
					header := createWavHeader(len(audioBuffer))
					if _, err := audioFile.Write(header); err != nil {
						log.Println("Error writing WAV header:", err)
					}
					if _, err := audioFile.Write(audioBuffer); err != nil {
						log.Println("Error writing WAV data:", err)
					}
					audioFile.Close()
					log.Printf("Saved audio to call_%s.wav", streamSid)
				}
				return
			}

			log.Println(string(msg))
			// Parse incoming Twilio message
			var twilioMsg TwilioMedia
			if err := json.Unmarshal(msg, &twilioMsg); err != nil {
				log.Println("JSON parse error:", err)
				continue
			}
			if twilioMsg.Event == "start" {
				streamSid = twilioMsg.StreamSid
				audioFile, err = os.Create("call_" + streamSid + ".wav")
				if err != nil {
					log.Println("Error creating WAV file:", err)
					return
				}
				audioBuffer = nil // Reset buffer for new call
				log.Printf("Created file for call_%s.wav", streamSid)
			}

			// Save inbound audio from "media" event
			if twilioMsg.Event == "media" && twilioMsg.Media.Track == "inbound" {
				audioData, err := base64.StdEncoding.DecodeString(twilioMsg.Media.Payload)
				if err != nil {
					log.Println("Base64 decode error:", err)
					continue
				}
				audioBuffer = append(audioBuffer, audioData...)
				log.Println("Appended audio chunk, total length:", len(audioBuffer))
			}

			if twilioMsg.Event == "start" {
				response := map[string]interface{}{
					"event":     "media",
					"streamSid": twilioMsg.StreamSid,
					"media": map[string]string{
						"payload": base64Audio,
					},
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
				log.Println("Sent μ-law audio response")
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
