package bybit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client Bybit REST 客户端（B所 / 壳子账户）
type Client struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

// NewClient 创建 Bybit REST 客户端
func NewClient(baseURL, apiKey, apiSecret string) *Client {
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ---------- 公共数据结构 ----------

// OrderBook 订单簿快照
type OrderBook struct {
	Bids [][]string `json:"b"` // [[price, size], ...]
	Asks [][]string `json:"a"`
}

// BestPrice 最优买卖价
type BestPrice struct {
	BidPrice float64
	BidSize  float64
	AskPrice float64
	AskSize  float64
}

// Position 持仓信息
type Position struct {
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"` // Buy / Sell
	Size          string  `json:"size"`
	EntryPrice    string  `json:"avgPrice"`
	UnrealizedPnl string  `json:"unrealisedPnl"`
	SizeFloat     float64 // 解析后的数量
}

// Account 账户信息
type Account struct {
	TotalEquity     float64
	AvailableMargin float64
}

// Order 订单信息
type Order struct {
	OrderID     string `json:"orderId"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`   // Buy / Sell
	OrderType   string `json:"orderType"` // Limit / Market
	Price       string `json:"price"`
	Qty         string `json:"qty"`
	CumExecQty  string `json:"cumExecQty"`
	OrderStatus string `json:"orderStatus"` // New / Filled / Cancelled
	CreatedTime string `json:"createdTime"`
}

// PlaceOrderReq 下单请求
type PlaceOrderReq struct {
	Category    string `json:"category"`              // linear（USDT永续）
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`                  // Buy / Sell
	OrderType   string `json:"orderType"`             // Limit / Market
	Qty         string `json:"qty"`
	Price       string `json:"price,omitempty"`
	TimeInForce string `json:"timeInForce,omitempty"` // GTC / IOC / FOK / PostOnly
	ReduceOnly  bool   `json:"reduceOnly"`
	OrderLinkID string `json:"orderLinkId,omitempty"` // 自定义订单ID
}

// ---------- 签名工具 ----------

// sign 生成 Bybit V5 签名
// 签名规范：timestamp + apiKey + recvWindow + queryString/body
func (c *Client) sign(timestamp, recvWindow, payload string) string {
	raw := timestamp + c.apiKey + recvWindow + payload
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

// request 发送带签名的 HTTP 请求（Bybit V5 API）
func (c *Client) request(method, path string, payload interface{}) ([]byte, error) {
	var bodyStr string
	var bodyReader io.Reader

	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyStr = string(b)
		bodyReader = bytes.NewBufferString(bodyStr)
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"
	sig := c.sign(timestamp, recvWindow, bodyStr)

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BAPI-API-KEY", c.apiKey)
	req.Header.Set("X-BAPI-SIGN", sig)
	req.Header.Set("X-BAPI-TIMESTAMP", timestamp)
	req.Header.Set("X-BAPI-RECV-WINDOW", recvWindow)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}

	// 检查 Bybit 业务错误码
	var baseResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
	}
	if err := json.Unmarshal(data, &baseResp); err == nil {
		if baseResp.RetCode != 0 {
			return nil, fmt.Errorf("Bybit 错误 %d: %s", baseResp.RetCode, baseResp.RetMsg)
		}
	}

	return data, nil
}

// ---------- 公开接口 ----------

// GetOrderBook 获取订单簿（公开接口，无需签名）
func (c *Client) GetOrderBook(symbol string) (*OrderBook, error) {
	url := fmt.Sprintf("%s/v5/market/orderbook?category=linear&symbol=%s&limit=5", c.baseURL, symbol)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		RetCode int `json:"retCode"`
		Result  struct {
			B [][]string `json:"b"` // bids
			A [][]string `json:"a"` // asks
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.RetCode != 0 {
		return nil, fmt.Errorf("Bybit 获取订单簿失败，retCode=%d", result.RetCode)
	}

	return &OrderBook{
		Bids: result.Result.B,
		Asks: result.Result.A,
	}, nil
}

// GetBestPrice 获取最优买卖价
func (c *Client) GetBestPrice(symbol string) (*BestPrice, error) {
	ob, err := c.GetOrderBook(symbol)
	if err != nil {
		return nil, err
	}
	if len(ob.Bids) == 0 || len(ob.Asks) == 0 {
		return nil, fmt.Errorf("Bybit 订单簿为空")
	}

	bp := &BestPrice{}
	fmt.Sscanf(ob.Bids[0][0], "%f", &bp.BidPrice)
	fmt.Sscanf(ob.Bids[0][1], "%f", &bp.BidSize)
	fmt.Sscanf(ob.Asks[0][0], "%f", &bp.AskPrice)
	fmt.Sscanf(ob.Asks[0][1], "%f", &bp.AskSize)
	return bp, nil
}

// ---------- 私有接口 ----------

// GetAccount 获取统一账户余额
func (c *Client) GetAccount() (*Account, error) {
	path := "/v5/account/wallet-balance?accountType=UNIFIED"
	data, err := c.request("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			List []struct {
				TotalEquity     string `json:"totalEquity"`
				AvailableMargin string `json:"totalAvailableBalance"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if len(result.Result.List) == 0 {
		return nil, fmt.Errorf("账户数据为空")
	}

	acc := &Account{}
	fmt.Sscanf(result.Result.List[0].TotalEquity, "%f", &acc.TotalEquity)
	fmt.Sscanf(result.Result.List[0].AvailableMargin, "%f", &acc.AvailableMargin)
	return acc, nil
}

// GetPositions 获取持仓列表
func (c *Client) GetPositions(symbol string) ([]Position, error) {
	path := fmt.Sprintf("/v5/position/list?category=linear&symbol=%s", symbol)
	data, err := c.request("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			List []Position `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	// 解析 Size 字段
	for i := range result.Result.List {
		fmt.Sscanf(result.Result.List[i].Size, "%f", &result.Result.List[i].SizeFloat)
	}

	return result.Result.List, nil
}

// PlaceOrder 下单（B所壳子账户执行套利）
func (c *Client) PlaceOrder(req *PlaceOrderReq) (*Order, error) {
	data, err := c.request("POST", "/v5/order/create", req)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			OrderID     string `json:"orderId"`
			OrderLinkID string `json:"orderLinkId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return &Order{
		OrderID: result.Result.OrderID,
		Symbol:  req.Symbol,
		Side:    req.Side,
	}, nil
}

// CancelOrder 撤销单个订单
func (c *Client) CancelOrder(symbol, orderID string) error {
	req := map[string]string{
		"category": "linear",
		"symbol":   symbol,
		"orderId":  orderID,
	}
	_, err := c.request("POST", "/v5/order/cancel", req)
	return err
}

// CancelAllOrders 撤销某交易对所有订单
func (c *Client) CancelAllOrders(symbol string) error {
	req := map[string]string{
		"category": "linear",
		"symbol":   symbol,
	}
	_, err := c.request("POST", "/v5/order/cancel-all", req)
	return err
}

// GetOpenOrders 获取当前挂单
func (c *Client) GetOpenOrders(symbol string) ([]Order, error) {
	path := fmt.Sprintf("/v5/order/realtime?category=linear&symbol=%s", symbol)
	data, err := c.request("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			List []Order `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Result.List, nil
}
