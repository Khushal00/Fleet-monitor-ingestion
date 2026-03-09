package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeDeadline  = 10 * time.Second
	readDeadline   = 60 * time.Second
	pingInterval   = 30 * time.Second
	sendBufferSize = 256
)

type Client struct {
	hub     *Hub
	conn    *websocket.Conn
	fleetID string
	send    chan []byte
	done    chan struct{}
	once    sync.Once
}

func newClient(hub *Hub, conn *websocket.Conn, fleetID string) *Client {
	return &Client{
		hub:     hub,
		conn:    conn,
		fleetID: fleetID,
		send:    make(chan []byte, sendBufferSize),
		done:    make(chan struct{}),
	}
}

func (c *Client) close() {
	c.once.Do(func() {
		close(c.done)
		c.hub.unregister <- c
	})
}

func (c *Client) readPump() {
	defer c.close()

	c.conn.SetReadDeadline(time.Now().Add(readDeadline))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(readDeadline))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
			) {
				log.Printf("ws: unexpected close for fleet %s: %v", c.fleetID, err)
			}
			return
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(writeDeadline),
		)
		c.conn.Close()
		c.close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
			if !ok {
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("ws: write error for fleet %s: %v", c.fleetID, err)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
			appPing, _ := json.Marshal(newPingEvent())
			c.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
			if err := c.conn.WriteMessage(websocket.TextMessage, appPing); err != nil {
				return
			}

		case <-c.done:
			return
		}
	}
}

func (c *Client) enqueue(msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}
