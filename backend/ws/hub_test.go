package ws

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestHub_ShopIsolationAndBroadcast(t *testing.T) {
	hub := NewHub(discardLogger())
	go hub.Run()
	defer hub.Shutdown()

	c1 := &Client{
		hub:    hub,
		shopID: "shop-1",
		send:   make(chan []byte, 10),
	}
	c2 := &Client{
		hub:    hub,
		shopID: "shop-2",
		send:   make(chan []byte, 10),
	}

	hub.registerClient(c1)
	hub.registerClient(c2)

	// Wait briefly for registration to take effect
	time.Sleep(20 * time.Millisecond)
	if count := hub.ClientCount(); count != 2 {
		t.Fatalf("expected 2 clients, got %d", count)
	}

	// Publish to shop-1
	if !hub.Publish("shop-1", EventTransactionCreated, map[string]string{"msg": "sale for shop 1"}) {
		t.Fatal("expected Publish to return true")
	}

	// c1 should receive it, c2 should not
	select {
	case msg := <-c1.send:
		if len(msg) == 0 {
			t.Fatal("expected non-empty message for c1")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("c1 timed out waiting for event")
	}

	select {
	case <-c2.send:
		t.Fatal("c2 should not receive events for shop-1")
	case <-time.After(50 * time.Millisecond):
		// Success
	}

	// Unregister c1
	hub.unregisterClient(c1)
	time.Sleep(20 * time.Millisecond)
	if count := hub.ClientCount(); count != 1 {
		t.Fatalf("expected 1 client after unregister, got %d", count)
	}
}

func TestHub_SlowClientEviction(t *testing.T) {
	hub := NewHub(discardLogger())
	go hub.Run()
	defer hub.Shutdown()

	// Client with small buffer size 2
	c := &Client{
		hub:    hub,
		shopID: "shop-slow",
		send:   make(chan []byte, 2),
	}
	hub.registerClient(c)
	time.Sleep(20 * time.Millisecond)

	// Fill the client's buffer
	c.send <- []byte("msg1")
	c.send <- []byte("msg2")

	// Next broadcast should trigger eviction because c.send is full
	hub.Publish("shop-slow", EventTransactionCreated, map[string]string{"msg": "overflow"})

	// Wait for fanout to evict client
	time.Sleep(50 * time.Millisecond)

	if count := hub.ClientCount(); count != 0 {
		t.Fatalf("expected 0 clients after eviction, got %d", count)
	}

	// Drain the two messages, then the channel should be closed
	<-c.send
	<-c.send
	select {
	case _, ok := <-c.send:
		if ok {
			t.Fatal("expected client.send channel to be closed upon eviction")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for client.send channel closure")
	}
}

func TestHub_ConcurrentPublishAndRegistration(t *testing.T) {
	hub := NewHub(discardLogger())
	go hub.Run()
	defer hub.Shutdown()

	var wg sync.WaitGroup
	const numClients = 10
	const numBroadcasts = 20

	clients := make([]*Client, numClients)
	for i := 0; i < numClients; i++ {
		shopID := "shop-even"
		if i%2 != 0 {
			shopID = "shop-odd"
		}
		clients[i] = &Client{
			hub:    hub,
			shopID: shopID,
			send:   make(chan []byte, 100),
		}
		hub.registerClient(clients[i])
	}

	// Concurrently broadcast
	for i := 0; i < numBroadcasts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			shopID := "shop-even"
			if idx%2 != 0 {
				shopID = "shop-odd"
			}
			hub.Publish(shopID, EventTransactionCreated, idx)
		}(i)
	}

	// Concurrently drain clients to prevent blocking
	doneDraining := make(chan struct{})
	go func() {
		for {
			select {
			case <-doneDraining:
				return
			default:
				for _, c := range clients {
					select {
					case <-c.send:
					default:
					}
				}
				time.Sleep(1 * time.Millisecond)
			}
		}
	}()

	wg.Wait()
	close(doneDraining)

	for _, c := range clients {
		hub.unregisterClient(c)
	}
	time.Sleep(20 * time.Millisecond)
	if count := hub.ClientCount(); count != 0 {
		t.Fatalf("expected 0 clients after all unregister, got %d", count)
	}
}

func TestHub_Shutdown(t *testing.T) {
	hub := NewHub(discardLogger())
	go hub.Run()

	c := &Client{
		hub:    hub,
		shopID: "shop-shutdown",
		send:   make(chan []byte, 10),
	}
	hub.registerClient(c)
	time.Sleep(20 * time.Millisecond)

	hub.Shutdown()
	time.Sleep(20 * time.Millisecond)

	// Broadcast after shutdown should fail
	ok := hub.Publish("shop-shutdown", EventTransactionCreated, nil)
	if ok {
		t.Fatal("expected Publish to return false after Shutdown")
	}

	// c.send should be closed
	select {
	case _, open := <-c.send:
		if open {
			t.Fatal("expected c.send to be closed on hub shutdown")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for client.send to close after shutdown")
	}
}

