package ws

import (
	"time"

	"github.com/gorilla/websocket"
)

const (
	// sendBuffer is how many events may queue for one dashboard before the
	// hub treats it as stalled and disconnects it.
	sendBuffer = 32

	// writeWait bounds a single write. A dashboard on a slow shop connection
	// must not be able to hold the write goroutine open indefinitely.
	writeWait = 10 * time.Second

	// pongWait is how long a client may go silent before the connection is
	// considered dead. This is what reaps half-open connections — the case
	// where a phone loses signal and no close frame ever arrives.
	pongWait = 60 * time.Second

	// pingPeriod must be shorter than pongWait, or the server times the
	// client out before it has a chance to answer. The conventional 9/10
	// leaves room for one lost ping.
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is deliberately tiny: V1 is server-push only, so the
	// only legitimate inbound traffic is control frames.
	maxMessageSize = 1024
)

// Client is one connected dashboard.
//
// Two goroutines own it, matching the library's contract of one concurrent
// reader and one concurrent writer: ReadPump consumes inbound frames and
// notices disconnects, WritePump drains the send channel. Neither goroutine
// writes to the other's state.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	shopID string

	// send carries marshalled events. The hub owns the write end of its
	// lifecycle and closes it to signal a clean stop; nothing else may close
	// or write to it directly.
	send chan []byte
}

// newClient registers a freshly upgraded connection with the hub.
func (h *Hub) newClient(conn *websocket.Conn, shopID string) *Client {
	client := &Client{
		hub:    h,
		conn:   conn,
		shopID: shopID,
		send:   make(chan []byte, sendBuffer),
	}
	h.registerClient(client)
	return client
}

// ReadPump consumes inbound frames until the connection fails.
//
// The dashboard sends no commands in V1, so payloads are discarded. Reading
// still has to happen: it is what processes pong and close frames, and what
// surfaces a dropped connection, which in turn triggers the unregister that
// the client would otherwise leak.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.unregisterClient(c)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		// Any inbound frame is evidence of life; extend the deadline.
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.hub.log.Warn("ws: read failed", "shop_id", c.shopID, "error", err)
			}
			return
		}
		// Inbound payload ignored: see the method comment.
	}
}

// WritePump sends queued events and keeps the connection warm with pings.
//
// It is the only writer, so the library's single-writer constraint holds. Each
// event goes out as one text frame, which keeps the browser side a simple
// one-message-per-event parse.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel: it is shutting down or has
				// dropped this client. Say goodbye properly.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
