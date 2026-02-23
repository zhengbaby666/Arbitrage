package risk

import (
	"fmt"
	"log"
	"sync"
	"time"

	"arb/config"
)

// Controller 风控控制器
type Controller struct {
	cfg config.RiskConfig

	mu sync.Mutex

	// 当日累计盈亏（USDC）
	dailyPnL float64

	// 连续亏损次数
	consecutiveLoss int

	// 熔断状态
	halted    bool
	haltedMsg string

	// 当日重置时间
	dayStart time.Time
}

// NewController 创建风控控制器
func NewController(cfg config.RiskConfig) *Controller {
	return &Controller{
		cfg:      cfg,
		dayStart: todayStart(),
	}
}

// Check 检查是否允许下单，返回 nil 表示允许，否则返回拒绝原因
func (c *Controller) Check(availableBalance float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 检查是否需要重置当日统计
	c.resetIfNewDay()

	// 熔断检查
	if c.halted {
		return fmt.Errorf("熔断中: %s", c.haltedMsg)
	}

	// 账户余额检查
	if availableBalance < c.cfg.MinBalanceUSDC {
		msg := fmt.Sprintf("可用余额 %.2f USDC 低于最低要求 %.2f USDC", availableBalance, c.cfg.MinBalanceUSDC)
		c.halt(msg)
		return fmt.Errorf(msg)
	}

	// 当日亏损检查
	if c.dailyPnL < -c.cfg.MaxDailyLossUSDC {
		msg := fmt.Sprintf("当日亏损 %.2f USDC 超过限制 %.2f USDC", -c.dailyPnL, c.cfg.MaxDailyLossUSDC)
		c.halt(msg)
		return fmt.Errorf(msg)
	}

	// 连续亏损检查
	if c.consecutiveLoss >= c.cfg.MaxConsecutiveLoss {
		msg := fmt.Sprintf("连续亏损 %d 次超过限制 %d 次", c.consecutiveLoss, c.cfg.MaxConsecutiveLoss)
		c.halt(msg)
		return fmt.Errorf(msg)
	}

	return nil
}

// RecordTrade 记录一笔交易结果（pnl 为正表示盈利，负表示亏损）
func (c *Controller) RecordTrade(pnl float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.dailyPnL += pnl

	if pnl < 0 {
		c.consecutiveLoss++
		log.Printf("[风控] 亏损交易，连续亏损次数: %d，当日累计PnL: %.2f USDC", c.consecutiveLoss, c.dailyPnL)
	} else {
		c.consecutiveLoss = 0
		log.Printf("[风控] 盈利交易，当日累计PnL: %.2f USDC", c.dailyPnL)
	}
}

// DailyPnL 返回当日累计盈亏
func (c *Controller) DailyPnL() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dailyPnL
}

// IsHalted 返回是否处于熔断状态
func (c *Controller) IsHalted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.halted
}

// Reset 人工重置熔断状态（需人工干预后调用）
func (c *Controller) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.halted = false
	c.haltedMsg = ""
	c.consecutiveLoss = 0
	log.Println("[风控] 熔断状态已人工重置")
}

// ---- 内部方法 ----

func (c *Controller) halt(msg string) {
	if !c.halted {
		c.halted = true
		c.haltedMsg = msg
		log.Printf("[风控] 触发熔断: %s", msg)
	}
}

func (c *Controller) resetIfNewDay() {
	now := time.Now()
	if now.After(c.dayStart.Add(24 * time.Hour)) {
		c.dailyPnL = 0
		c.consecutiveLoss = 0
		c.halted = false
		c.haltedMsg = ""
		c.dayStart = todayStart()
		log.Println("[风控] 新的一天，重置当日统计")
	}
}

func todayStart() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}
