package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config 全局配置
type Config struct {
	// Apex Pro（A所）配置
	Apex ApexConfig `yaml:"apex"`

	// Bybit（B所 / 壳子账户）配置
	Bybit BybitConfig `yaml:"bybit"`

	// Apex 交易对，例如 BTC-USDC
	ApexSymbol string `yaml:"apex_symbol"`

	// Bybit 交易对，例如 BTCUSDT
	BybitSymbol string `yaml:"bybit_symbol"`

	// 套利策略参数
	Strategy StrategyConfig `yaml:"strategy"`

	// 风控参数
	RiskControl RiskConfig `yaml:"risk_control"`
}

// ApexConfig Apex Pro REST/WS 接口配置（A所）
type ApexConfig struct {
	BaseURL    string `yaml:"base_url"`
	WsURL      string `yaml:"ws_url"`
	APIKey     string `yaml:"api_key"`
	APISecret  string `yaml:"api_secret"`
	Passphrase string `yaml:"passphrase"`
}

// BybitConfig Bybit REST/WS 接口配置（B所 / 壳子账户）
type BybitConfig struct {
	BaseURL   string `yaml:"base_url"`
	WsURL     string `yaml:"ws_url"`
	APIKey    string `yaml:"api_key"`
	APISecret string `yaml:"api_secret"`
}

// StrategyConfig 套利策略参数
type StrategyConfig struct {
	// 触发套利的最小价差（USDC）
	MinSpreadUSDC float64 `yaml:"min_spread_usdc"`

	// 单笔下单量（合约张数）
	OrderSize float64 `yaml:"order_size"`

	// 最大净持仓量（合约张数）
	MaxPosition float64 `yaml:"max_position"`

	// 套利方向检查间隔（毫秒）
	CheckIntervalMs int `yaml:"check_interval_ms"`

	// 盈利目标（USDC）
	TakeProfitUSDC float64 `yaml:"take_profit_usdc"`

	// 止损（USDC）
	StopLossUSDC float64 `yaml:"stop_loss_usdc"`

	// 价格精度（小数位数）
	PricePrecision int `yaml:"price_precision"`

	// 数量精度（小数位数）
	SizePrecision int `yaml:"size_precision"`

	// 对冲模式：true=双腿对冲，false=单腿
	HedgeMode bool `yaml:"hedge_mode"`

	// 对冲滑点容忍（USDC）
	HedgeSlippageUSDC float64 `yaml:"hedge_slippage_usdc"`
}

// RiskConfig 风控配置
type RiskConfig struct {
	// 单日最大亏损（USDC）
	MaxDailyLossUSDC float64 `yaml:"max_daily_loss_usdc"`

	// 最大连续亏损次数
	MaxConsecutiveLoss int `yaml:"max_consecutive_loss"`

	// 账户最低余额（USDC）
	MinBalanceUSDC float64 `yaml:"min_balance_usdc"`
}

// Load 从 YAML 文件加载配置，支持环境变量覆盖
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// 环境变量优先级高于配置文件（Apex）
	if v := os.Getenv("APEX_API_KEY"); v != "" {
		cfg.Apex.APIKey = v
	}
	if v := os.Getenv("APEX_API_SECRET"); v != "" {
		cfg.Apex.APISecret = v
	}
	if v := os.Getenv("APEX_PASSPHRASE"); v != "" {
		cfg.Apex.Passphrase = v
	}

	// 环境变量优先级高于配置文件（Bybit）
	if v := os.Getenv("BYBIT_API_KEY"); v != "" {
		cfg.Bybit.APIKey = v
	}
	if v := os.Getenv("BYBIT_API_SECRET"); v != "" {
		cfg.Bybit.APISecret = v
	}

	return cfg, nil
}
