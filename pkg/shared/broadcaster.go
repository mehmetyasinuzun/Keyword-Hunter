package shared

import (
	"encoding/json"
	"sync"
)

// LogMessage frontend'e gidecek log mesajı
type LogMessage struct {
	Type      string `json:"type"`      // info, success, error, engine_start, engine_end
	Message   string `json:"message"`   // Gösterilecek mesaj
	Engine    string `json:"engine"`    // İlgili motor (varsa)
	Timestamp int64  `json:"timestamp"` // Unix timestamp
}

// Broadcaster SSE yayıncısı
type Broadcaster struct {
	clients    map[chan string]bool
	register   chan chan string
	unregister chan chan string
	broadcast  chan string
	mu         sync.Mutex
}

var Streamer = NewBroadcaster()

func NewBroadcaster() *Broadcaster {
	b := &Broadcaster{
		clients:    make(map[chan string]bool),
		register:   make(chan chan string),
		unregister: make(chan chan string),
		broadcast:  make(chan string),
	}
	go b.run()
	return b
}

func (b *Broadcaster) run() {
	for {
		select {
		case client := <-b.register:
			b.mu.Lock()
			b.clients[client] = true
			b.mu.Unlock()
		case client := <-b.unregister:
			b.mu.Lock()
			if _, ok := b.clients[client]; ok {
				delete(b.clients, client)
				close(client)
			}
			b.mu.Unlock()
		case message := <-b.broadcast:
			b.mu.Lock()
			for client := range b.clients {
				select {
				case client <- message:
				default:
					close(client)
					delete(b.clients, client)
				}
			}
			b.mu.Unlock()
		}
	}
}

// Register yeni bir istemci kaydeder
func (b *Broadcaster) Register(client chan string) {
	b.register <- client
}

// Unregister istemciyi siler
func (b *Broadcaster) Unregister(client chan string) {
	b.unregister <- client
}

// BroadcastLog log mesajı yayınlar
func (b *Broadcaster) BroadcastLog(msgType, message, engine string) {
	logMsg := LogMessage{
		Type:      msgType,
		Message:   message,
		Engine:    engine,
		Timestamp: 0, // Frontend handles timestamp or simply current time
	}
	
	jsonBytes, _ := json.Marshal(logMsg)
	b.broadcast <- string(jsonBytes)
}
