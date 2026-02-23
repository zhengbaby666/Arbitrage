package strategy

import (
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	apexPkg "arb/apex"
	bybitPkg "arb/bybit"
	"arb/config"
	"arb/risk"
)

// ArbDirection 套利方向
type ArbDirection int

const (
	DirectionNone  ArbDirection = iota
	DirectionLong               // Apex 价格低于 Bybit → 在 Apex 买入（开多），Bybit 卖出（对冲）
	DirectionShort              // Apex 价格高于 Bybit → 在 Apex 卖出（开空），Bybit 买入（对冲）
)

// ArbEngine 套利引擎
// 逻辑：
//
//	A所（Apex）= 流动性来源，监控其订单簿价格
//	B所（Bybit）= 执行套利下单
//	共用流动性池：Apex 和 Bybit 共享深度，价差出现时立即套利
//	赚钱方式：当两所价差 > min_spread 时，低买高卖，吃掉外部做市商的差价
type ArbEngine struct {
	cfg         *config.Config
	apexClient  *apexPkg.Client
	apexWs      *apexPkg.WsClient
	bybitClient *bybitPkg.Client
	bybitWs     *bybitPkg.WsClient
	riskCtrl    *risk.Controller

	// 最新行情（原子更新）
	apexBid  atomic.Value // float64
	apexAsk  atomic.Value // float64
	bybitBid atomic.Value // float64
	bybitAsk atomic.Value // float64

	// 当前持仓（Bybit B所）
	posMu    sync.Mutex
	position float64 // 正数=多头，负数=空头

	// 累计盈亏
	totalPnL float64
	pnlMu    sync.Mutex

	// 运行控制
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewArbEngine 创建套利引擎
func NewArbEngine(cfg *config.Config) (*ArbEngine, error) {
	e := &ArbEngine{
		cfg:         cfg,
		apexClient:  apexPkg.NewClient(cfg.Apex.BaseURL, cfg.Apex.APIKey, cfg.Apex.APISecret, cfg.Apex.Passphrase),
		apexWs:      apexPkg.NewWsClient(cfg.Apex.WsURL),
		bybitClient: bybitPkg.NewClient(cfg.Bybit.BaseURL, cfg.Bybit.APIKey, cfg.Bybit.APISecret),
		bybitWs:     bybitPkg.NewWsClient(cfg.Bybit.WsURL),
		riskCtrl:    risk.NewController(cfg.RiskControl),
		stopCh:      make(chan struct{}),
	}

	// 初始化行情为 0
	e.apexBid.Store(0.0)
	e.apexAsk.Store(0.0)
	e.bybitBid.Store(0.0)
	e.bybitAsk.Store(0.0)

	return e, nil
}

// Start 启动套利引擎
func (e *ArbEngine) Start() error {
	log.Printf("=== 套利引擎启动 ===")
	log.Printf("A所（Apex）: %s  交易对: %s", e.cfg.Apex.BaseURL, e.cfg.ApexSymbol)
	log.Printf("B所（Bybit）: %s  交易对: %s", e.cfg.Bybit.BaseURL, e.cfg.BybitSymbol)
	log.Printf("最小价差: %.2f USDC  单笔量: %.4f  对冲模式: %v",
		e.cfg.Strategy.MinSpreadUSDC, e.cfg.Strategy.OrderSize, e.cfg.Strategy.HedgeMode)

	// 连接 Apex WebSocket（A所行情）
	if err := e.apexWs.Connect(); err != nil {
		return fmt.Errorf("Apex WS 连接失败: %w", err)
	}
	if err := e.apexWs.SubscribeOrderBook(e.cfg.ApexSymbol, e.onApexOrderBook); err != nil {
		return fmt.Errorf("Apex 订单簿订阅失败: %w", err)
	}

	// 连接 Bybit WebSocket（B所行情）
	if err := e.bybitWs.Connect(); err != nil {
		return fmt.Errorf("Bybit WS 连接失败: %w", err)
	}
	if err := e.bybitWs.SubscribeOrderBook(e.cfg.BybitSymbol, e.onBybitOrderBook); err != nil {
		return fmt.Errorf("Bybit 订单簿订阅失败: %w", err)
	}

	// 等待行情就绪
	log.Println("等待行情数据就绪...")
	if err := e.waitForMarketData(10 * time.Second); err != nil {
		return err
	}
	log.Println("行情数据就绪，开始套利监控")

	// 启动套利主循环
	e.wg.Add(1)
	go e.arbLoop()

	// 启动状态打印
	e.wg.Add(1)
	go e.statusLoop()

	return nil
}

// Stop 停止套利引擎，撤销所有挂单
func (e *ArbEngine) Stop() {
	log.Println("正在停止套利引擎...")
	close(e.stopCh)
	e.wg.Wait()

	// 撤销 Bybit 所有挂单
	if err := e.bybitClient.CancelAllOrders(e.cfg.BybitSymbol); err != nil {
		log.Printf("[停止] 撤销 Bybit 挂单失败: %v", err)
	} else {
		log.Println("[停止] Bybit 挂单已全部撤销")
	}

	e.apexWs.Close()
	e.bybitWs.Close()

	e.pnlMu.Lock()
	log.Printf("=== 套利引擎已停止，累计PnL: %.4f USDC ===", e.totalPnL)
	e.pnlMu.Unlock()
}

// ---- 行情回调 ----

// onApexOrderBook 处理 Apex 订单簿更新（A所行情）
func (e *ArbEngine) onApexOrderBook(ob *apexPkg.WsOrderBook) {
	if len(ob.Bids) > 0 && len(ob.Asks) > 0 {
		var bid, ask float64
		fmt.Sscanf(ob.Bids[0][0], "%f", &bid)
		fmt.Sscanf(ob.Asks[0][0], "%f", &ask)
		e.apexBid.Store(bid)
		e.apexAsk.Store(ask)
	}
}

// onBybitOrderBook 处理 Bybit 订单簿更新（B所行情）
func (e *ArbEngine) onBybitOrderBook(ob *bybitPkg.WsOrderBook) {
	if len(ob.Bids) > 0 && len(ob.Asks) > 0 {
		var bid, ask float64
		fmt.Sscanf(ob.Bids[0][0], "%f", &bid)
		fmt.Sscanf(ob.Asks[0][0], "%f", &ask)
		e.bybitBid.Store(bid)
		e.bybitAsk.Store(ask)
	}
}

// ---- 套利主循环 ----

// arbLoop 套利主循环：持续检测价差，发现机会立即下单
func (e *ArbEngine) arbLoop() {
	defer e.wg.Done()

	interval := time.Duration(e.cfg.Strategy.CheckIntervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.checkAndTrade()
		}
	}
}

// checkAndTrade 检测价差并执行套利
func (e *ArbEngine) checkAndTrade() {
	// 获取最新行情
	apexBid := e.apexBid.Load().(float64)
	apexAsk := e.apexAsk.Load().(float64)
	bybitBid := e.bybitBid.Load().(float64)
	bybitAsk := e.bybitAsk.Load().(float64)

	if apexBid == 0 || apexAsk == 0 || bybitBid == 0 || bybitAsk == 0 {
		return // 行情未就绪
	}

	// 检查风控
	acc, err := e.bybitClient.GetAccount()
	if err != nil {
		log.Printf("[套利] 获取账户信息失败: %v", err)
		return
	}
	if err := e.riskCtrl.Check(acc.AvailableMargin); err != nil {
		log.Printf("[风控] 拒绝下单: %v", err)
		return
	}

	// 检查盈亏目标
	e.pnlMu.Lock()
	pnl := e.totalPnL
	e.pnlMu.Unlock()

	if pnl >= e.cfg.Strategy.TakeProfitUSDC {
		log.Printf("[套利] 达到盈利目标 %.2f USDC，停止套利", e.cfg.Strategy.TakeProfitUSDC)
		go e.Stop()
		return
	}
	if pnl <= -e.cfg.Strategy.StopLossUSDC {
		log.Printf("[套利] 触发止损 %.2f USDC，停止套利", e.cfg.Strategy.StopLossUSDC)
		go e.Stop()
		return
	}

	// 检查持仓限制
	e.posMu.Lock()
	pos := e.position
	e.posMu.Unlock()

	// ============================================================
	// 核心套利逻辑
	// ============================================================
	// 场景1：Apex 卖一价(Ask) < Bybit 买一价(Bid)
	//   → 在 Apex 买入（吃 Apex 卖单），同时在 Bybit 卖出（对冲）
	//   → 差价 = bybitBid - apexAsk（正值即为利润）
	//
	// 场景2：Apex 买一价(Bid) > Bybit 卖一价(Ask)
	//   → 在 Apex 卖出（吃 Apex 买单），同时在 Bybit 买入（对冲）
	//   → 差价 = apexBid - bybitAsk（正值即为利润）
	// ============================================================

	// 场景1：Apex 便宜，Bybit 贵 → 在 Apex 买，Bybit 卖
	spread1 := bybitBid - apexAsk
	if spread1 >= e.cfg.Strategy.MinSpreadUSDC && pos < e.cfg.Strategy.MaxPosition {
		log.Printf("[套利] 发现机会 场景1: Apex卖一=%.4f Bybit买一=%.4f 价差=%.4f USDC",
			apexAsk, bybitBid, spread1)
		e.executeLong(apexAsk, bybitBid, spread1)
		return
	}

	// 场景2：Apex 贵，Bybit 便宜 → 在 Apex 卖，Bybit 买
	spread2 := apexBid - bybitAsk
	if spread2 >= e.cfg.Strategy.MinSpreadUSDC && pos > -e.cfg.Strategy.MaxPosition {
		log.Printf("[套利] 发现机会 场景2: Apex买一=%.4f Bybit卖一=%.4f 价差=%.4f USDC",
			apexBid, bybitAsk, spread2)
		e.executeShort(apexBid, bybitAsk, spread2)
		return
	}
}

// executeLong 场景1：Apex 买入 + Bybit 卖出（对冲）
// 利润来源：bybitBid - apexAsk - 手续费
func (e *ArbEngine) executeLong(apexAsk, bybitBid, spread float64) {
	size := fmt.Sprintf("%.*f", e.cfg.Strategy.SizePrecision, e.cfg.Strategy.OrderSize)
	apexPrice := fmt.Sprintf("%.*f", e.cfg.Strategy.PricePrecision, apexAsk)
	bybitPrice := fmt.Sprintf("%.*f", e.cfg.Strategy.PricePrecision, bybitBid)

	// 腿1：在 Apex（A所）买入
	apexOrder, err := e.apexClient.PlaceOrder(&apexPkg.PlaceOrderReq{
		Symbol:      e.cfg.ApexSymbol,
		Side:        "BUY",
		Type:        "LIMIT",
		Size:        size,
		Price:       apexPrice,
		TimeInForce: "IOC", // 立即成交或取消，避免挂单风险
		ReduceOnly:  false,
	})
	if err != nil {
		log.Printf("[套利] Apex 买入失败: %v", err)
		return
	}
	log.Printf("[套利] Apex 买入成功 OrderID=%s 价格=%s 数量=%s", apexOrder.ID, apexPrice, size)

	// 腿2（对冲）：在 Bybit（B所）卖出
	if e.cfg.Strategy.HedgeMode {
		bybitOrder, err := e.bybitClient.PlaceOrder(&bybitPkg.PlaceOrderReq{
			Category:    "linear",
			Symbol:      e.cfg.BybitSymbol,
			Side:        "Sell",
			OrderType:   "Limit",
			Qty:         size,
			Price:       bybitPrice,
			TimeInForce: "IOC",
			ReduceOnly:  false,
		})
		if err != nil {
			log.Printf("[套利] Bybit 对冲卖出失败: %v（Apex 腿已成交，注意风险）", err)
			return
		}
		log.Printf("[套利] Bybit 对冲卖出成功 OrderID=%s 价格=%s 数量=%s", bybitOrder.OrderID, bybitPrice, size)
	}

	// 更新持仓和盈亏
	e.posMu.Lock()
	e.position += e.cfg.Strategy.OrderSize
	e.posMu.Unlock()

	estimatedPnL := spread * e.cfg.Strategy.OrderSize
	e.pnlMu.Lock()
	e.totalPnL += estimatedPnL
	e.pnlMu.Unlock()

	e.riskCtrl.RecordTrade(estimatedPnL)
	log.Printf("[套利] 场景1完成，预估本次PnL=%.4f USDC，累计PnL=%.4f USDC", estimatedPnL, e.totalPnL)
}

// executeShort 场景2：Apex 卖出 + Bybit 买入（对冲）
// 利润来源：apexBid - bybitAsk - 手续费
func (e *ArbEngine) executeShort(apexBid, bybitAsk, spread float64) {
	size := fmt.Sprintf("%.*f", e.cfg.Strategy.SizePrecision, e.cfg.Strategy.OrderSize)
	apexPrice := fmt.Sprintf("%.*f", e.cfg.Strategy.PricePrecision, apexBid)
	bybitPrice := fmt.Sprintf("%.*f", e.cfg.Strategy.PricePrecision, bybitAsk)

	// 腿1：在 Apex（A所）卖出
	apexOrder, err := e.apexClient.PlaceOrder(&apexPkg.PlaceOrderReq{
		Symbol:      e.cfg.ApexSymbol,
		Side:        "SELL",
		Type:        "LIMIT",
		Size:        size,
		Price:       apexPrice,
		TimeInForce: "IOC",
		ReduceOnly:  false,
	})
	if err != nil {
		log.Printf("[套利] Apex 卖出失败: %v", err)
		return
	}
	log.Printf("[套利] Apex 卖出成功 OrderID=%s 价格=%s 数量=%s", apexOrder.ID, apexPrice, size)

	// 腿2（对冲）：在 Bybit（B所）买入
	if e.cfg.Strategy.HedgeMode {
		bybitOrder, err := e.bybitClient.PlaceOrder(&bybitPkg.PlaceOrderReq{
			Category:    "linear",
			Symbol:      e.cfg.BybitSymbol,
			Side:        "Buy",
			OrderType:   "Limit",
			Qty:         size,
			Price:       bybitPrice,
			TimeInForce: "IOC",
			ReduceOnly:  false,
		})
		if err != nil {
			log.Printf("[套利] Bybit 对冲买入失败: %v（Apex 腿已成交，注意风险）", err)
			return
		}
		log.Printf("[套利] Bybit 对冲买入成功 OrderID=%s 价格=%s 数量=%s", bybitOrder.OrderID, bybitPrice, size)
	}

	// 更新持仓和盈亏
	e.posMu.Lock()
	e.position -= e.cfg.Strategy.OrderSize
	e.posMu.Unlock()

	estimatedPnL := spread * e.cfg.Strategy.OrderSize
	e.pnlMu.Lock()
	e.totalPnL += estimatedPnL
	e.pnlMu.Unlock()

	e.riskCtrl.RecordTrade(estimatedPnL)
	log.Printf("[套利] 场景2完成，预估本次PnL=%.4f USDC，累计PnL=%.4f USDC", estimatedPnL, e.totalPnL)
}

// ---- 辅助方法 ----

// waitForMarketData 等待两所行情数据都就绪
func (e *ArbEngine) waitForMarketData(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		apexBid := e.apexBid.Load().(float64)
		bybitBid := e.bybitBid.Load().(float64)
		if apexBid > 0 && bybitBid > 0 {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待行情超时（%v），请检查 WebSocket 连接", timeout)
}

// statusLoop 定期打印运行状态
func (e *ArbEngine) statusLoop() {
	defer e.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			apexBid := e.apexBid.Load().(float64)
			apexAsk := e.apexAsk.Load().(float64)
			bybitBid := e.bybitBid.Load().(float64)
			bybitAsk := e.bybitAsk.Load().(float64)

			e.posMu.Lock()
			pos := e.position
			e.posMu.Unlock()

			e.pnlMu.Lock()
			pnl := e.totalPnL
			e.pnlMu.Unlock()

			// 计算当前两所价差
			spread1 := bybitBid - apexAsk
			spread2 := apexBid - bybitAsk

			log.Printf("[状态] Apex: bid=%.4f ask=%.4f | Bybit: bid=%.4f ask=%.4f | 价差1=%.4f 价差2=%.4f | 持仓=%.4f | 累计PnL=%.4f USDC | 日PnL=%.4f USDC",
				apexBid, apexAsk, bybitBid, bybitAsk,
				spread1, spread2,
				math.Abs(pos), pnl, e.riskCtrl.DailyPnL())
		}
	}
}
