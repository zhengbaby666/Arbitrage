package bybit

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// WsOrderBook Bybit WebSocket 推送的订单簿数据
type WsOrderBook struct {
	Symbol string     `json:"s"`
	Bids   [][]string `json:"b"`
	Asks   [][]string `json:"a"`
	Ts     int64      `json:"ts"`
}

// WsClient Bybit WebSocket 客户端（支持断线重连）
type WsClient struct {
	wsURL string

	mu   sync.Mutex
	conn *websocket.Conn

	// 订阅注册表
	subsMu sync.RWMutex
	subs   []wsSubscription

	// 连接状态
	connected      atomic.Bool
	reconnectCount atomic.Int64
	lastMsgAt      atomic.Value // time.Time

	// 内部控制
	done     chan struct{}
	reconnCh chan struct{}
}

type wsSubscription struct {
	topic string
	cb    func(data []byte)
}

const (
	bybitWsInitialBackoff = 1 * time.Second
	bybitWsMaxBackoff     = 30 * time.Second
	bybitWsPingInterval   = 20 * time.Second
	bybitWsDialTimeout    = 10 * time.Second
)

// NewWsClient 创建 Bybit WebSocket 客户端
func NewWsClient(wsURL string) *WsClient {
	w := &WsClient{
		wsURL:    wsURL,
		done:     make(chan struct{}),
		reconnCh: make(chan struct{}, 1),
	}
	w.lastMsgAt.Store(time.Time{})
	return w
}

// Connect 建立初始连接并启动后台 goroutine
func (w *WsClient) Connect() error {
	if err := w.dial(); err != nil {
		return err
	}
	go w.reconnectLoop()
	return nil
}

// SubscribeOrderBook 订阅订单簿频道
func (w *WsClient) SubscribeOrderBook(symbol string, cb func(ob *WsOrderBook)) error {
	// Bybit V5 公共频道格式：orderbook.1.BTCUSDT
	topic := fmt.Sprintf("orderbook.1.%s", symbol)

	w.subsMu.Lock()
	w.subs = append(w.subs, wsSubscription{
		topic: topic,
		cb: func(data []byte) {
			var ob WsOrderBook
			if err := json.Unmarshal(data, &ob); err != nil {
				log.Printf("[Bybit WS] 解析订单簿数据失败: %v", err)
				return
			}
			cb(&ob)
		},
	})
	w.subsMu.Unlock()

	return w.sendSubscribe(topic)
}

// IsReady 返回当前是否已连接
func (w *WsClient) IsReady() bool {
	return w.connected.Load()
}

// Close 关闭客户端
func (w *WsClient) Close() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	w.mu.Lock()
	if w.conn != nil {
		_ = w.conn.Close()
	}
	w.mu.Unlock()
}

// ---- 内部方法 ----

func (w *WsClient) dial() error {
	dialer := websocket.Dialer{HandshakeTimeout: bybitWsDialTimeout}
	conn, _, err := dialer.Dial(w.wsURL, nil)
	if err != nil {
		return fmt.Errorf("[Bybit WS] 连接失败: %w", err)
	}

	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()

	w.connected.Store(true)
	log.Printf("[Bybit WS] 连接成功: %s", w.wsURL)

	go w.readLoop(conn)
	go w.pingLoop(conn)
	return nil
}

func (w *WsClient) reconnectLoop() {
	backoff := bybitWsInitialBackoff
	for {
		select {
		case <-w.done:
			return
		case <-w.reconnCh:
			w.connected.Store(false)
			count := w.reconnectCount.Add(1)
			log.Printf("[Bybit WS] 检测到断线，第 %d 次重连，等待 %v ...", count, backoff)

			select {
			case <-w.done:
				return
			case <-time.After(backoff):
			}

			if err := w.dial(); err != nil {
				log.Printf("[Bybit WS] 重连失败: %v", err)
				backoff *= 2
				if backoff > bybitWsMaxBackoff {
					backoff = bybitWsMaxBackoff
				}
				select {
				case w.reconnCh <- struct{}{}:
				default:
				}
				continue
			}

			backoff = bybitWsInitialBackoff
			w.resubscribeAll()
		}
	}
}

func (w *WsClient) readLoop(conn *websocket.Conn) {
	defer func() {
		select {
		case <-w.done:
			return
		default:
			select {
			case w.reconnCh <- struct{}{}:
			default:
			}
		}
	}()

	for {
		select {
		case <-w.done:
			return
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-w.done:
			default:
				log.Printf("[Bybit WS] 读取错误（将触发重连）: %v", err)
			}
			return
		}

		w.lastMsgAt.Store(time.Now())

		// Bybit V5 消息格式：{"topic":"orderbook.1.BTCUSDT","type":"snapshot","data":{...}}
		var envelope struct {
			Topic string          `json:"topic"`
			Type  string          `json:"type"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			continue
		}
		if envelope.Topic == "" {
			continue
		}

		w.subsMu.RLock()
		for _, s := range w.subs {
			if s.topic == envelope.Topic {
				s.cb(envelope.Data)
				break
			}
		}
		w.subsMu.RUnlock()
	}
}

// pingLoop 定时发送 Bybit 心跳（Bybit 要求发送 JSON ping）
func (w *WsClient) pingLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(bybitWsPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			ping := map[string]string{"op": "ping"}
			w.mu.Lock()
			err := conn.WriteJSON(ping)
			w.mu.Unlock()

			if err != nil {
				log.Printf("[Bybit WS] Ping 发送失败: %v", err)
				return
			}
		}
	}
}

func (w *WsClient) resubscribeAll() {
	w.subsMu.RLock()
	defer w.subsMu.RUnlock()
	for _, s := range w.subs {
		if err := w.sendSubscribe(s.topic); err != nil {
			log.Printf("[Bybit WS] 恢复订阅 %s 失败: %v", s.topic, err)
		} else {
			log.Printf("[Bybit WS] 已恢复订阅: %s", s.topic)
		}
	}
}

func (w *WsClient) sendSubscribe(topic string) error {
	msg := map[string]interface{}{
		"op":   "subscribe",
		"args": []string{topic},
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn == nil {
		return fmt.Errorf("连接尚未建立")
	}
	return w.conn.WriteJSON(msg)
}
