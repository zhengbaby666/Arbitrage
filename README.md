# Apex-Bybit 流动性套利程序

## 套利原理

> **壳子**：指没有散户、只有做市商画 K 线的 meme 币。这类币的价格完全由做市商控制，没有真实二级市场的价格发现机制。正是因为如此，在 A 所现货市场主动买入就能直接推高价格，并通过共享流动性池传导至 B 所永续合约。



### 模型一：被动价差套利（基础模型）

```
A所（Apex Pro）= 流动性来源，监控订单簿价格
        ↓
  共享流动性池（Apex + Bybit 共用深度）
        ↓
  B所（Bybit）= 执行套利下单
        ↓
  外部做市商 → 价差即为利润
```

**赚钱逻辑：**

| 场景 | 条件 | 操作 | 利润来源 |
|------|------|------|----------|
| 场景1 | Apex 卖一价 < Bybit 买一价 | Apex 买入 + Bybit 卖出对冲 | `bybitBid - apexAsk - 手续费` |
| 场景2 | Apex 买一价 > Bybit 卖一价 | Apex 卖出 + Bybit 买入对冲 | `apexBid - bybitAsk - 手续费` |

两所共用流动性池，价差出现时立即通过 Bybit 执行套利，吃掉外部做市商的差价。

---

### 模型二：跨交易所联动套利 + 做市商被动抬价模型（进阶模型）

> **一句话总结：在 A 现货制造价格波动，在 B 合约提前埋伏吃这波价格传导。**

```
用户操作：
 ├─ 步骤1：在 A 所（Apex）现货主动买入 → 推高现货价格
 │
 ├─ 步骤2：A 的流动性同步给 B 所（Bybit）
 │         └─ 共享流动性池将 A 的价格变动传导至 B
 │
 ├─ 步骤3：B 所永续合约价格被 A 的现货价格带着走
 │         └─ 做市商被动跟随抬价（无法抗拒的价格传导）
 │
 └─ 步骤4：提前在 B 所做多永续合约 → 等 A 推价完成 → 平仓获利
```

**执行时序：**

| 步骤 | 操作 | 说明 |
|------|------|------|
| ① 埋伏 | 在 B 所（Bybit）永续合约**提前做多** | 在推价前建仓，成本最低 |
| ② 推价 | 在 A 所（Apex）现货**持续买入** | 制造价格上涨，推高现货价 |
| ③ 传导 | A 的价格通过共享流动性池**同步至 B** | 做市商被动跟随抬高 B 的永续价格 |
| ④ 平仓 | B 所永续价格上涨后**平多仓** | 吃掉价格传导带来的涨幅利润 |

**利润来源：**

```
利润 = B所永续平仓价 - B所永续开仓价 - A所现货推价成本 - 手续费

其中：
  A所推价成本 = 现货买入均价 × 数量（推价后可在 A 所卖出回收）
  B所利润     = 永续价格涨幅 × 合约张数 × 杠杆
  净利润       = B所利润 - A所推价净损耗 - 双边手续费
```

**关键前提：**
- Apex 和 Bybit **共用流动性池**，A 的现货价格变动能有效传导至 B 的永续价格
- B 所做多仓位需在 A 所推价**之前**建立，否则成本过高
- A 所推价买入量需足够大，能突破做市商的挂单深度，形成有效价格推动

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
│   ├── client.go           # Bybit REST 客户端（B所）
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

### Bybit 配置（B所）

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

### v1.3.0 — 2026-02-23

**概念修正**

- 修正项目中对「壳子」的错误理解：壳子是指没有散户、只有做市商画 K 线的 meme 币，而非账户/接口概念
- 移除 `README.md`、`bybit/client.go`、`strategy/engine.go`、`config.yaml`、`config/config.go` 中所有「壳子账户」的错误描述
- 在 README 套利原理章节首部补充壳子的正确定义



**初始版本**

- 新增 `apex/client.go`：Apex Pro REST 客户端，支持下单、撤单、查询持仓/账户
- 新增 `apex/ws.go`：Apex Pro WebSocket 客户端，支持订单簿订阅、断线重连
- 新增 `bybit/client.go`：Bybit V5 REST 客户端，支持下单、撤单、查询持仓/账户
- 新增 `bybit/ws.go`：Bybit WebSocket 客户端，支持订单簿订阅、断线重连
- 新增 `strategy/engine.go`：套利引擎核心，实现双所价差检测与双腿对冲下单
- 新增 `risk/controller.go`：风控控制器，支持日亏损熔断、连续亏损熔断、余额检查
- 新增 `config/config.go`：配置结构体，支持 YAML 加载和环境变量覆盖
- 新增 `config.yaml`：完整配置模板
- 新增 `main.go`：程序入口

### v1.2.0 — 2026-02-23

**新增模型二：跨交易所联动套利 + 做市商被动抬价**

- 新增 `strategy/model2.go`：模型二引擎，实现五阶段状态机
  - `PhaseIdle` → `PhaseAmbush`（Bybit 做多埋伏）
  - `PhaseAmbush` → `PhasePushPrice`（Apex 现货持续推价）
  - `PhasePushPrice` → `PhaseWaiting`（等待价格传导至 Bybit）
  - `PhaseWaiting` → `PhaseTakeProfit`（达到止盈/止损触发平仓）
  - `PhaseTakeProfit` → `PhaseCooldown`（平仓 + 回收 Apex 现货 + 冷却）
- 更新 `config/config.go`：新增 `Model2Config` 结构体、`mode` 字段
- 更新 `config.yaml`：新增 `mode` 运行模式选择（1=模型一，2=模型二）、`model2` 配置块
- 更新 `main.go`：根据 `mode` 字段自动选择启动模型一或模型二引擎

**模型二关键配置说明（`config.yaml` → `model2` 块）：**

| 字段 | 说明 | 建议值 |
|------|------|--------|
| `ambush_size` | Bybit 埋伏做多仓位（合约张数） | `0.01` |
| `push_order_size` | Apex 每轮推价买入量 | `0.005` |
| `push_rounds` | 推价总轮次 | `5` |
| `push_interval_ms` | 推价间隔（毫秒） | `500` |
| `push_price_slippage` | 推价滑点比例（0.001=0.1%） | `0.001` |
| `take_profit_per_unit` | 每张合约止盈（USDC） | `5.0` |
| `stop_loss_per_unit` | 每张合约止损（USDC） | `3.0` |
| `transmission_timeout_sec` | 等待传导超时（秒） | `60` |
| `cooldown_sec` | 每轮冷却时间（秒） | `30` |

**切换到模型二运行：**
```yaml
# config.yaml
mode: 2
```

### v1.1.0 — 2026-02-23

**文档更新**

- 补充「模型二：跨交易所联动套利 + 做市商被动抬价模型」说明
- 新增执行时序表：埋伏建仓 → A所推价 → 价格传导 → B所平仓
- 新增利润计算公式及关键前提说明
