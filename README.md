# Apex-Bybit 流动性套利程序

## 套利原理

```
A所（Apex Pro）= 流动性来源，监控订单簿价格
        ↓
  共享流动性池（Apex + Bybit 共用深度）
        ↓
  B所（Bybit）= 壳子账户，执行套利下单
        ↓
  外部做市商 → 价差即为利润
```

**赚钱逻辑：**

| 场景 | 条件 | 操作 | 利润来源 |
|------|------|------|----------|
| 场景1 | Apex 卖一价 < Bybit 买一价 | Apex 买入 + Bybit 卖出对冲 | `bybitBid - apexAsk - 手续费` |
| 场景2 | Apex 买一价 > Bybit 卖一价 | Apex 卖出 + Bybit 买入对冲 | `apexBid - bybitAsk - 手续费` |

两所共用流动性池，价差出现时立即通过壳子账户（Bybit）执行套利，吃掉外部做市商的差价。

---

## 项目结构

```
Arbitrage/
├── main.go                 # 程序入口
├── config.yaml             # 配置文件
├── go.mod                  # Go 模块依赖
├── config/
│   └── config.go           # 配置结构体 & 加载逻辑
├── apex/
│   ├── client.go           # Apex Pro REST 客户端（A所）
│   └── ws.go               # Apex Pro WebSocket 客户端（A所行情）
├── bybit/
│   ├── client.go           # Bybit REST 客户端（B所壳子账户）
│   └── ws.go               # Bybit WebSocket 客户端（B所行情）
├── strategy/
│   └── engine.go           # 套利引擎核心逻辑
└── risk/
    └── controller.go       # 风控控制器（熔断/止损/余额检查）
```

---

## 配置说明（config.yaml）

### Apex Pro 配置（A所）

| 字段 | 说明 | 示例 |
|------|------|------|
| `apex.base_url` | REST 接口地址（主网/测试网） | `https://pro.apex.exchange` |
| `apex.ws_url` | WebSocket 地址 | `wss://pro.apex.exchange/realtime` |
| `apex.api_key` | Apex API Key | 从 Apex Pro 后台获取 |
| `apex.api_secret` | Apex API Secret | 从 Apex Pro 后台获取 |
| `apex.passphrase` | Apex 口令 | 从 Apex Pro 后台获取 |

### Bybit 配置（B所 / 壳子账户）

| 字段 | 说明 | 示例 |
|------|------|------|
| `bybit.base_url` | REST 接口地址（主网/测试网） | `https://api.bybit.com` |
| `bybit.ws_url` | WebSocket 地址 | `wss://stream.bybit.com/v5/public/linear` |
| `bybit.api_key` | Bybit API Key | 从 Bybit 后台获取 |
| `bybit.api_secret` | Bybit API Secret | 从 Bybit 后台获取 |

### 交易对配置

| 字段 | 说明 | 示例 |
|------|------|------|
| `apex_symbol` | Apex 交易对格式 | `BTC-USDC` |
| `bybit_symbol` | Bybit 交易对格式 | `BTCUSDT` |

### 套利策略参数

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `strategy.min_spread_usdc` | 触发套利的最小价差（USDC），低于此值不套利 | `1.0` |
| `strategy.order_size` | 单笔下单量（合约张数） | `0.001` |
| `strategy.max_position` | 最大净持仓量（合约张数），超过后停止同向开仓 | `0.01` |
| `strategy.check_interval_ms` | 价差检查间隔（毫秒），越小越灵敏 | `200` |
| `strategy.take_profit_usdc` | 盈利目标（USDC），达到后自动停止 | `100.0` |
| `strategy.stop_loss_usdc` | 止损（USDC），超过后自动停止 | `30.0` |
| `strategy.price_precision` | 价格精度（小数位数） | `1` |
| `strategy.size_precision` | 数量精度（小数位数） | `3` |
| `strategy.hedge_mode` | 对冲模式：`true`=双腿对冲，`false`=单腿 | `true` |
| `strategy.hedge_slippage_usdc` | 对冲腿允许的最大滑点（USDC） | `0.5` |

### 风控参数

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `risk_control.max_daily_loss_usdc` | 单日最大亏损（USDC），超过后熔断停止 | `50.0` |
| `risk_control.max_consecutive_loss` | 最大连续亏损次数，超过后需人工重置 | `5` |
| `risk_control.min_balance_usdc` | 账户最低可用余额（USDC），低于此值停止交易 | `200.0` |

---

## 环境变量（优先级高于配置文件）

```bash
export APEX_API_KEY="your_apex_api_key"
export APEX_API_SECRET="your_apex_api_secret"
export APEX_PASSPHRASE="your_apex_passphrase"
export BYBIT_API_KEY="your_bybit_api_key"
export BYBIT_API_SECRET="your_bybit_api_secret"
```

---

## 运行说明

### 1. 安装依赖

```bash
cd Arbitrage
go mod tidy
```

### 2. 填写配置

编辑 `config.yaml`，填入 Apex 和 Bybit 的 API 密钥：

```yaml
apex:
  api_key: "你的 Apex API Key"
  api_secret: "你的 Apex API Secret"
  passphrase: "你的 Apex Passphrase"

bybit:
  api_key: "你的 Bybit API Key"
  api_secret: "你的 Bybit API Secret"
```

### 3. 编译运行

```bash
# 直接运行
go run main.go

# 编译后运行
go build -o arb main.go
./arb
```

### 4. 测试网运行（推荐先测试）

修改 `config.yaml` 中的地址为测试网：

```yaml
apex:
  base_url: "https://testnet.pro.apex.exchange"

bybit:
  base_url: "https://api-testnet.bybit.com"
```

### 5. 停止程序

```bash
Ctrl+C
```

程序收到信号后会自动撤销所有 Bybit 挂单，安全退出。

---

## 成本计算

```
单次套利利润 = 价差 × 下单量 - 手续费
             = (bybitBid - apexAsk) × orderSize - fee

手续费估算（以 BTC 为例）：
  Apex Taker 费率：约 0.05%
  Bybit Taker 费率：约 0.055%
  合计：约 0.105%

盈亏平衡价差 = 成交价 × 0.105% × 2 ≈ 成交价 × 0.21%
  例如 BTC=60000，盈亏平衡价差 ≈ 60000 × 0.0021 ≈ 126 USDC
```

> **建议**：`min_spread_usdc` 设置为盈亏平衡价差的 1.5~2 倍，确保每次套利都有利润。

---

## 风控说明

程序内置三层风控：

1. **账户余额检查**：可用余额低于 `min_balance_usdc` 时停止下单
2. **单日亏损熔断**：当日累计亏损超过 `max_daily_loss_usdc` 时触发熔断
3. **连续亏损熔断**：连续亏损次数超过 `max_consecutive_loss` 时触发熔断，需人工重置

---

## 注意事项

1. **双腿风险**：套利为双腿操作，若一腿成交另一腿失败，会产生裸露头寸，需人工处理
2. **滑点风险**：使用 IOC 订单，未成交部分自动取消，避免挂单风险
3. **API 权限**：Bybit API Key 需开启「合约交易」权限；Apex API Key 需开启「交易」权限
4. **测试优先**：建议先在测试网验证策略，再切换主网

---

## 代码变更记录

### v1.0.0 — 2026-02-23

**初始版本**

- 新增 `apex/client.go`：Apex Pro REST 客户端，支持下单、撤单、查询持仓/账户
- 新增 `apex/ws.go`：Apex Pro WebSocket 客户端，支持订单簿订阅、断线重连
- 新增 `bybit/client.go`：Bybit V5 REST 客户端（壳子账户），支持下单、撤单、查询持仓/账户
- 新增 `bybit/ws.go`：Bybit WebSocket 客户端，支持订单簿订阅、断线重连
- 新增 `strategy/engine.go`：套利引擎核心，实现双所价差检测与双腿对冲下单
- 新增 `risk/controller.go`：风控控制器，支持日亏损熔断、连续亏损熔断、余额检查
- 新增 `config/config.go`：配置结构体，支持 YAML 加载和环境变量覆盖
- 新增 `config.yaml`：完整配置模板
- 新增 `main.go`：程序入口
