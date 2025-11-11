package broker

import (
	"context"
	"decentralized-api/cosmosclient"
	"decentralized-api/logging"
	"decentralized-api/mlnodeclient"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/productscience/inference/x/inference/types"
)

type WebSocketMessage struct {
	Type  string          `json:"type"`
	Batch json.RawMessage `json:"batch"`
	ID    string          `json:"id"`
}

type AckMessage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type WebSocketClient struct {
	nodeID  string
	wsURL   string
	handler *BatchHandler

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewWebSocketClient(nodeID string, pocURL string, recorder cosmosclient.CosmosMessageClient) *WebSocketClient {
	ctx, cancel := context.WithCancel(context.Background())

	// If caller passed a full ws:// or wss:// URL, use it as-is. Otherwise, convert and append default path.
	var wsURL string
	if len(pocURL) >= 5 && (pocURL[:5] == "ws://" || (len(pocURL) >= 6 && pocURL[:6] == "wss://")) {
		wsURL = pocURL
	} else {
		wsURL = convertHTTPToWSURL(pocURL) + "/api/v1/pow/ws"
	}

	return &WebSocketClient{
		nodeID:  nodeID,
		wsURL:   wsURL,
		handler: NewBatchHandler(recorder),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (c *WebSocketClient) Start() {
	c.wg.Add(1)
	go c.run()
}

func (c *WebSocketClient) Stop() {
	logging.Debug("WebSocket: canceling context", types.Nodes, "nodeId", c.nodeID)
	c.cancel()
	logging.Debug("WebSocket: waiting for context to finish", types.Nodes, "nodeId", c.nodeID)
	c.wg.Wait()
	logging.Debug("WebSocket: stopped", types.Nodes, "nodeId", c.nodeID)
}

func (c *WebSocketClient) run() {
	defer c.wg.Done()

	for {
		// Context-aware dial with timeout
		dialCtx, dialCancel := context.WithTimeout(c.ctx, 10*time.Second)
		conn, _, err := websocket.Dial(dialCtx, c.wsURL, nil)
		dialCancel()

		if err != nil {
			// Check if parent context was canceled
			if c.ctx.Err() != nil {
				return
			}
			logging.Debug("WebSocket. Failed to connect", types.PoC, "nodeId", c.nodeID, "error", err)

			// Wait before reconnecting, respecting context cancellation
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(reconnectInterval()):
				continue
			}
		}

		logging.Debug("WebSocket. Connected to node", types.PoC, "nodeId", c.nodeID, "wsURL", c.wsURL)

		c.readProcessAckLoop(conn)

		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
}

func (c *WebSocketClient) readProcessAckLoop(conn *websocket.Conn) {
	// Set read limit
	conn.SetReadLimit(64 << 20) // 64MB limit

	for {
		// Read with context and timeout - this respects context cancellation natively
		readCtx, readCancel := context.WithTimeout(c.ctx, 60*time.Second)
		msgType, rawMessage, err := conn.Read(readCtx)
		readCancel()

		if err != nil {
			// Check if parent context was canceled
			if c.ctx.Err() != nil {
				return
			}
			logging.Debug("WebSocket. Read error, will reconnect", types.PoC, "nodeId", c.nodeID, "error", err)
			return
		}

		// Only process text/binary messages
		if msgType != websocket.MessageText && msgType != websocket.MessageBinary {
			continue
		}

		messageID, err := c.processMessage(rawMessage)
		if err != nil {
			logging.Error("WebSocket. Failed to process message", types.PoC, "nodeId", c.nodeID, "error", err)
			// Continue reading; bad messages shouldn't tear down the connection
			continue
		}

		if messageID != "" {
			// Write acknowledgment with context and timeout
			writeCtx, writeCancel := context.WithTimeout(c.ctx, 10*time.Second)
			ack := AckMessage{Type: "ack", ID: messageID}
			err := wsjson.Write(writeCtx, conn, ack)
			writeCancel()

			if err != nil {
				logging.Error("WebSocket. Failed to send acknowledgment", types.PoC,
					"nodeId", c.nodeID, "messageId", messageID, "error", err)
				// Tear down and reconnect on write failure
				return
			}
			logging.Debug("WebSocket. Sent acknowledgment", types.PoC, "nodeId", c.nodeID, "messageId", messageID)
		}
	}
}

func (c *WebSocketClient) processMessage(rawMessage []byte) (string, error) {
	var msg WebSocketMessage
	if err := json.Unmarshal(rawMessage, &msg); err != nil {
		return "", fmt.Errorf("failed to unmarshal message: %w", err)
	}

	switch msg.Type {
	case "generated":
		var batch mlnodeclient.ProofBatch
		if err := json.Unmarshal(msg.Batch, &batch); err != nil {
			return "", fmt.Errorf("failed to unmarshal generated batch: %w", err)
		}
		if err := c.handler.HandleGeneratedBatch(c.nodeID, batch); err != nil {
			return "", fmt.Errorf("failed to handle generated batch: %w", err)
		}
		return msg.ID, nil

	case "validated":
		var batch mlnodeclient.ValidatedBatch
		if err := json.Unmarshal(msg.Batch, &batch); err != nil {
			return "", fmt.Errorf("failed to unmarshal validated batch: %w", err)
		}
		if err := c.handler.HandleValidatedBatch(batch); err != nil {
			return "", fmt.Errorf("failed to handle validated batch: %w", err)
		}
		return msg.ID, nil

	default:
		return "", fmt.Errorf("unknown message type: %s", msg.Type)
	}
}

func convertHTTPToWSURL(httpURL string) string {
	if len(httpURL) >= 7 && httpURL[:7] == "http://" {
		return "ws://" + httpURL[7:]
	}
	if len(httpURL) >= 8 && httpURL[:8] == "https://" {
		return "wss://" + httpURL[8:]
	}
	return fmt.Sprintf("ws://%s", httpURL)
}

func reconnectInterval() time.Duration {
	base := 3 * time.Second
	jitter := time.Duration(rand.Intn(2000)) * time.Millisecond
	return base + jitter
}
