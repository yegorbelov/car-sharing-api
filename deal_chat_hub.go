package main

import (
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
)

type dealChatHub struct {
	mu    sync.RWMutex
	rooms map[int64]map[*websocket.Conn]struct{}
}

func newDealChatHub() *dealChatHub {
	return &dealChatHub{rooms: make(map[int64]map[*websocket.Conn]struct{})}
}

func (h *dealChatHub) register(dealID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[dealID] == nil {
		h.rooms[dealID] = make(map[*websocket.Conn]struct{})
	}
	h.rooms[dealID][conn] = struct{}{}
}

func (h *dealChatHub) unregister(dealID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[dealID]
	if room == nil {
		return
	}
	delete(room, conn)
	if len(room) == 0 {
		delete(h.rooms, dealID)
	}
}

func (h *dealChatHub) broadcastMessage(dealID int64, msg dealMessageRow) {
	payload, err := json.Marshal(map[string]any{
		"type":    "new_message",
		"message": msg,
	})
	if err != nil {
		return
	}

	h.mu.RLock()
	room := h.rooms[dealID]
	conns := make([]*websocket.Conn, 0, len(room))
	for c := range room {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		if err := c.WriteMessage(websocket.TextMessage, payload); err != nil {
			_ = c.Close()
			h.unregister(dealID, c)
		}
	}
}
