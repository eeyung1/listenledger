package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
)

// publishBuffer bounds how many events may be queued for the fan-out
// goroutine before publishers are refused. Deep enough to absorb a burst of
// transactions, shallow enough that a stalled hub is noticed rather than
// growing without limit.
const publishBuffer = 256

// targeted is an event on its way to one shop's clients, marshalled once.
type targeted struct {
	shopID  string
	payload []byte
}

// Hub fans events out to connected dashboards, partitioned by shop.
//
// The registry is owned by a single goroutine (Run): register, unregister and
// publish arrive over channels, so no lock is needed around the client map.
// ShopID partitioning is the tenancy boundary — a broadcast for one business
// iterates only that business's clients, so a bug upstream cannot leak one
// shop's sales into another's dashboard.
//
// Run must be running before any client registers, or registration blocks.
type Hub struct {
	register   chan *Client
	unregister chan *Client
	publish    chan targeted
	done       chan struct{}
	stopOnce   sync.Once

	// shops is read and written only by the Run goroutine.
	shops map[string]map[*Client]struct{}

	seqs  map[string]*atomic.Int64
	seqMu sync.RWMutex

	clients  atomic.Int64
	dropped  atomic.Int64
	shutdown atomic.Bool

	log *slog.Logger
}

// NewHub returns a hub that is not yet running; call Run in its own goroutine.
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		publish:    make(chan targeted, publishBuffer),
		done:       make(chan struct{}),
		shops:      make(map[string]map[*Client]struct{}),
		seqs:       make(map[string]*atomic.Int64),
		log:        logger,
	}
}

// Run owns the client registry until Shutdown is called. It never returns
// before closing every client's send channel, so writers see a clean stop
// rather than a dangling connection.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			shop := h.shops[client.shopID]
			if shop == nil {
				shop = make(map[*Client]struct{})
				h.shops[client.shopID] = shop
			}
			shop[client] = struct{}{}
			h.clients.Add(1)
			h.log.Info("ws: client connected",
				"shop_id", client.shopID,
				"shop_clients", len(shop),
				"total_clients", h.clients.Load())

		case client := <-h.unregister:
			h.removeClient(client)

		case message := <-h.publish:
			h.fanout(message)

		case <-h.done:
			h.closeAll()
			return
		}
	}
}

// Broadcast queues an already-built event for delivery. See Publish for the
// higher-level entry point.
func (h *Hub) Broadcast(event Event) bool {
	if h.shutdown.Load() {
		return false
	}
	if event.ShopID == "" {
		h.log.Error("ws: refusing to broadcast event with no shop_id", "type", event.Type)
		return false
	}

	payload, err := json.Marshal(event)
	if err != nil {
		h.log.Error("ws: marshal event", "type", event.Type, "shop_id", event.ShopID, "error", err)
		return false
	}

	select {
	case h.publish <- targeted{shopID: event.ShopID, payload: payload}:
		return true
	case <-h.done:
		return false
	default:
		// The hub is saturated. Dropping is the right failure here: the
		// alternative is blocking an HTTP handler mid-request, and a
		// dashboard that missed an event recovers by resyncing on its next
		// reconnect. The counter and the log line are what make this
		// visible rather than silent.
		h.dropped.Add(1)
		h.log.Warn("ws: publish queue full, event dropped",
			"type", event.Type,
			"shop_id", event.ShopID,
			"dropped_total", h.dropped.Load())
		return false
	}
}

// ClientCount reports the number of connected dashboards across all shops.
func (h *Hub) ClientCount() int64 { return h.clients.Load() }

// DroppedCount reports how many events have been shed since start.
func (h *Hub) DroppedCount() int64 { return h.dropped.Load() }

// nextSeq generates the next monotonic sequence number for shopID.
func (h *Hub) nextSeq(shopID string) int64 {
	h.seqMu.Lock()
	defer h.seqMu.Unlock()
	c, ok := h.seqs[shopID]
	if !ok {
		c = &atomic.Int64{}
		h.seqs[shopID] = c
	}
	return c.Add(1)
}

// LastSeq reports the latest sequence number generated for shopID, or 0 if none.
func (h *Hub) LastSeq(shopID string) int64 {
	h.seqMu.RLock()
	defer h.seqMu.RUnlock()
	c, ok := h.seqs[shopID]
	if !ok {
		return 0
	}
	return c.Load()
}

// Shutdown stops the hub and disconnects every client. Safe to call more than
// once, so it can be deferred alongside an explicit call on the shutdown path.
func (h *Hub) Shutdown() {
	h.stopOnce.Do(func() {
		h.shutdown.Store(true)
		close(h.done)
	})
}

func (h *Hub) registerClient(client *Client) {
	select {
	case h.register <- client:
	case <-h.done:
	}
}

func (h *Hub) unregisterClient(client *Client) {
	select {
	case h.unregister <- client:
	case <-h.done:
	}
}

// fanout delivers one shop's payload to that shop's clients only.
//
// Runs on the Run goroutine, so it may mutate the registry directly.
func (h *Hub) fanout(message targeted) {
	shop := h.shops[message.shopID]
	if len(shop) == 0 {
		return
	}

	for client := range shop {
		select {
		case client.send <- message.payload:
		default:
			// This client is not draining its buffer: a dead connection the
			// OS has not reported yet, or a backgrounded tab. Drop it rather
			// than let one stalled dashboard delay every other client's
			// events. Closing the send channel is enough to unwind it —
			// WritePump closes the socket on its way out, ReadPump then fails
			// and unregisters, which is a no-op because the entry is gone.
			h.log.Warn("ws: client not draining, disconnecting",
				"shop_id", client.shopID,
				"shop_clients", len(shop))
			h.removeClient(client)
		}
	}
}

// removeClient deletes a client from the registry and closes its send channel.
//
// It is idempotent — a client dropped by fanout for not draining will still
// report its own disconnect, and the second attempt must not close the same
// channel twice. Must run on the Run goroutine.
func (h *Hub) removeClient(client *Client) {
	shop, ok := h.shops[client.shopID]
	if !ok {
		return
	}
	if _, ok := shop[client]; !ok {
		return
	}

	delete(shop, client)
	close(client.send)
	h.clients.Add(-1)

	if len(shop) == 0 {
		delete(h.shops, client.shopID)
	}

	h.log.Info("ws: client disconnected",
		"shop_id", client.shopID,
		"shop_clients", len(shop),
		"total_clients", h.clients.Load())
}

// closeAll terminates every connection on shutdown. Must run on the Run
// goroutine, and only after the done channel has fired.
func (h *Hub) closeAll() {
	for shopID, shop := range h.shops {
		for client := range shop {
			delete(shop, client)
			close(client.send)
			h.clients.Add(-1)
		}
		delete(h.shops, shopID)
	}
}
