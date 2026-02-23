package apex

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// WsOrderBook WebSocket 推送的订单簿数据
type WsOrderBook struct {
	Symbol string     `json:"symbol"`
	Bids   [][]string `json:"bids"`
	Asks   [][]string `json:"asks"`
	Ts     int64      `json:"ts"`
}

// subscription 保存一个订阅的元数据，用于断线后恢复
type subscription struct {
	topic string
	cb    func(data []byte)
}

// WsClient Apex Pro WebSocket 客户端（支持断线重连）
type WsClient struct {
	wsURL string

	mu   sync.Mutex
	conn *websocket.Conn

	// 订阅注册表（断线后自动恢复）
	subsMu sync.RWMutex
	subs   []subscription

	// 连接状态
	connected      atomic.Bool
	reconnectCount atomic.Int64
	lastPongAt     atomic.Value // time.Time
	lastMsgAt      atomic.Value // time.Time
	pingSeq        atomic.Int64
	rtt            atomic.Int64 // nanoseconds
	pingSentAt     sync.Map     // seq(string) → time.Time

	// 内部控制
	done     chan struct{}
	reconnCh chan struct{}
}

const (
	wsInitialBackoff = 1 * time.Second
	wsMaxBackoff     = 30 * time.Second
	wsPingInterval   = 20 * time.Second
	wsPongTimeout    = 10 * time.Second
	wsDialTimeout    = 10 * time.Second
)

// NewWsClient 创建 WebSocket 客户端
func NewWsClient(wsURL string) *WsClient {
	w := &WsClient{
		wsURL:    wsURL,
		done:     make(chan struct{}),
		reconnCh: make(chan struct{}, 1),
	}
	w.lastPongAt.Store(time.Time{})
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

// SubscribeOrderBook 订阅订单簿频道（断线重连后自动恢复）
func (w *WsClient) SubscribeOrderBook(symbol string, cb func(ob *WsOrderBook)) error {
	topic := fmt.Sprintf("orderbook.%s", symbol)

	w.subsMu.Lock()
	w.subs = append(w.subs, subscription{
		topic: topic,
		cb: func(data []byte) {
			var ob WsOrderBook
			if err := json.Unmarshal(data, &ob); err != nil {
				log.Printf("[Apex WS] 解析订单簿数据失败: %v", err)
				return
			}
			cb(&ob)
		},
	})
	w.subsMu.Unlock()

	return w.sendSubscribe(topic)
}

// IsReady 返回当前是否已连接且可用
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
	dialer := websocket.Dialer{HandshakeTimeout: wsDialTimeout}
	conn, _, err := dialer.Dial(w.wsURL, nil)
	if err != nil {
		return fmt.Errorf("[Apex WS] 连接失败: %w", err)
	}

	conn.SetPongHandler(func(appData string) error {
		now := time.Now()
		w.lastPongAt.Store(now)
		if sentVal, ok := w.pingSentAt.LoadAndDelete(appData); ok {
			if sentTime, ok2 := sentVal.(time.Time); ok2 {
				rtt := now.Sub(sentTime)
				w.rtt.Store(int64(rtt))
			}
		}
		return nil
	})

	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()

	w.connected.Store(true)
	log.Printf("[Apex WS] 连接成功: %s", w.wsURL)

	go w.readLoop(conn)
	go w.pingLoop(conn)
	return nil
}

func (w *WsClient) reconnectLoop() {
	backoff := wsInitialBackoff
	for {
		select {
		case <-w.done:
			return
		case <-w.reconnCh:
			w.connected.Store(false)
			count := w.reconnectCount.Add(1)
			log.Printf("[Apex WS] 检测到断线，第 %d 次重连，等待 %v ...", count, backoff)

			select {
			case <-w.done:
				return
			case <-time.After(backoff):
			}

			if err := w.dial(); err != nil {
				log.Printf("[Apex WS] 重连失败: %v", err)
				backoff *= 2
				if backoff > wsMaxBackoff {
					backoff = wsMaxBackoff
				}
				select {
				case w.reconnCh <- struct{}{}:
				default:
				}
				continue
			}

			backoff = wsInitialBackoff
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
				log.Printf("[Apex WS] 读取错误（将触发重连）: %v", err)
			}
			return
		}

		w.lastMsgAt.Store(time.Now())

		var envelope struct {
			Topic string          `json:"topic"`
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

func (w *WsClient) pingLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			if lastPong, ok := w.lastPongAt.Load().(time.Time); ok && !lastPong.IsZero() {
				if time.Since(lastPong) > wsPingInterval+wsPongTimeout {
					log.Printf("[Apex WS] Pong 超时，主动断线触发重连")
					_ = conn.Close()
					return
				}
			}

			seq := fmt.Sprintf("%d", w.pingSeq.Add(1))
			w.pingSentAt.Store(seq, time.Now())

			w.mu.Lock()
			err := conn.WriteMessage(websocket.PingMessage, []byte(seq))
			w.mu.Unlock()

			if err != nil {
				log.Printf("[Apex WS] Ping 发送失败: %v", err)
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
			log.Printf("[Apex WS] 恢复订阅 %s 失败: %v", s.topic, err)
		} else {
			log.Printf("[Apex WS] 已恢复订阅: %s", s.topic)
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
