package main

import (
	"encoding/json"
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
		Track     string `json:"track"`
		Payload   string `json:"payload"`
		Timestamp string `json:"timestamp"`
	} `json:"media"`
	Start struct {
		StreamSid string `json:"streamSid"`
		CallerID  string `json:"callSid"`
	} `json:"start"`
}

func main() {
	err := loadEnvFile(".env")
	if err != nil {
		log.Fatal("Error loading .env:", err)
	}
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		log.Fatal("Missing OPENAI_API_KEY in .env")
	}

	http.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		log.Println("WebSocket /stream hit")
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("WebSocket upgrade error:", err)
			return
		}
		defer ws.Close()

		// Connect to OpenAI Realtime API
		openAiWs, _, err := websocket.DefaultDialer.Dial("wss://api.openai.com/v1/realtime?model=gpt-realtime&temperature=0.8", http.Header{
			"Authorization": []string{"Bearer " + openAIKey},
		})
		if err != nil {
			log.Println("OpenAI WebSocket connection error:", err)
			return
		}
		defer openAiWs.Close()

		// Initialize OpenAI session
		sessionUpdate := map[string]interface{}{
			"type": "session.update",
			"session": map[string]interface{}{
				"type":              "realtime",
				"model":             "gpt-realtime",
				"output_modalities": []string{"audio"},
				"audio": map[string]interface{}{
					"input": map[string]interface{}{
						"format": map[string]interface{}{
							"type": "audio/pcmu",
						},
						"turn_detection": map[string]interface{}{
							"type": "server_vad",
						},
					},
					"output": map[string]interface{}{
						"format": map[string]interface{}{
							"type": "audio/pcmu",
						},
						"voice": "alloy",
					},
				},
				"instructions": "Eres un asistente IA que ayuda a las personas a reservar mesa en un restaurante. Limítate a hablar solo de este tema, para el restaurante Loopys. Hablas en castellano. Tu primer mensaje siempre será el siguiente: Hola! Has llamado al restaurante Loopys, soy una IA que te ayudará a reservar mesa. ¿Cuántas personas son?",
			},
		}
		if err := openAiWs.WriteJSON(sessionUpdate); err != nil {
			log.Println("Error sending OpenAI session update:", err)
			return
		}
		log.Println("Sent OpenAI session update")

		// Trigger AI response
		if err := openAiWs.WriteJSON(map[string]interface{}{"type": "response.create"}); err != nil {
			log.Println("Error sending response.create:", err)
			return
		}

		// Track state
		var streamSid string
		var responseStartTimestamp string
		var lastAssistantItem string
		markQueue := []string{}

		// Handle OpenAI WebSocket messages
		go func() {
			for {
				_, msg, err := openAiWs.ReadMessage()
				if err != nil {
					log.Println("OpenAI WebSocket read error:", err)
					return
				}
				var openAiMsg map[string]interface{}
				if err := json.Unmarshal(msg, &openAiMsg); err != nil {
					log.Println("OpenAI JSON parse error:", err)
					continue
				}
				eventType, _ := openAiMsg["type"].(string)
				log.Println("OpenAI event:", eventType)

				if eventType == "response.output_audio.delta" {
					delta, ok := openAiMsg["delta"].(string)
					if !ok || delta == "" {
						log.Println("Invalid or empty audio delta")
						continue
					}
					audioDelta := map[string]interface{}{
						"event":     "media",
						"streamSid": streamSid,
						"media": map[string]string{
							"payload": delta,
						},
					}
					if err := ws.WriteJSON(audioDelta); err != nil {
						log.Println("WebSocket write error:", err)
						return
					}
					log.Println("Sent AI audio to Twilio")

					if responseStartTimestamp == "" {
						if ts, ok := openAiMsg["timestamp"].(string); ok {
							responseStartTimestamp = ts
							log.Println("Set response start timestamp:", responseStartTimestamp)
						} else {
							log.Println("No timestamp in audio delta, using default")
						}
					}
					if itemID, ok := openAiMsg["item_id"].(string); ok {
						lastAssistantItem = itemID
					}
					// Send mark
					markEvent := map[string]interface{}{
						"event":     "mark",
						"streamSid": streamSid,
						"mark": map[string]string{
							"name": "responsePart",
						},
					}
					if err := ws.WriteJSON(markEvent); err != nil {
						log.Println("WebSocket mark write error:", err)
						return
					}
					markQueue = append(markQueue, "responsePart")
				} else if eventType == "input_audio_buffer.speech_started" {
					if len(markQueue) > 0 && lastAssistantItem != "" {
						truncateEvent := map[string]interface{}{
							"type":          "conversation.item.truncate",
							"item_id":       lastAssistantItem,
							"content_index": 0,
							"audio_end_ms":  0,
						}
						if err := openAiWs.WriteJSON(truncateEvent); err != nil {
							log.Println("Error sending truncate event:", err)
							continue
						}
						if err := ws.WriteJSON(map[string]interface{}{
							"event":     "clear",
							"streamSid": streamSid,
						}); err != nil {
							log.Println("WebSocket clear write error:", err)
							return
						}
						markQueue = []string{}
						lastAssistantItem = ""
						responseStartTimestamp = ""
						log.Println("Sent truncate and clear events")
					}
				}
			}
		}()

		// Handle Twilio WebSocket messages
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				log.Println("Twilio WebSocket read error:", err)
				return
			}
			var twilioMsg TwilioMedia
			if err := json.Unmarshal(msg, &twilioMsg); err != nil {
				log.Println("JSON parse error:", err)
				continue
			}
			log.Println("Twilio event:", twilioMsg.Event)

			switch twilioMsg.Event {
			case "start":
				streamSid = twilioMsg.Start.StreamSid
				log.Println("Stream started, StreamSid:", streamSid)
				log.Println("KABOOM", twilioMsg.Start.CallerID)
				responseStartTimestamp = ""
			case "media":
				if twilioMsg.Media.Track == "inbound" {
					audioAppend := map[string]interface{}{
						"type":  "input_audio_buffer.append",
						"audio": twilioMsg.Media.Payload,
					}
					if err := openAiWs.WriteJSON(audioAppend); err != nil {
						log.Println("Error sending audio to OpenAI:", err)
						continue
					}
					log.Println("Sent audio to OpenAI")
				}
			case "mark":
				if len(markQueue) > 0 {
					markQueue = markQueue[1:]
					log.Println("Processed mark, queue length:", len(markQueue))
				}
			}
		}
	})

	http.HandleFunc("/incomingCall", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Incoming call hit")
		twiml := `<?xml version="1.0" encoding="UTF-8"?>
        <Response>
            <Connect>
                <Stream url="wss://unminimizer.com/stream" statusCallback="https://unminimizer.com/streamStatus"/>
            </Connect>
            <Say>The stream has started.</Say>
        </Response>`
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(twiml))
	})

	http.HandleFunc("/streamStatus", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received stream status")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println("Error reading stream status body:", err)
			return
		}
		log.Println("Stream Status:", string(body))
		w.WriteHeader(http.StatusOK)
	})

	log.Fatal(http.ListenAndServe("0.0.0.0:5000", nil))
}
