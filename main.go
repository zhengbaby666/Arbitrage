package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"arb/config"
	"arb/strategy"
)

func main() {
	// 加载配置
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	log.Printf("启动 Apex-Bybit 套利程序")
	log.Printf("A所（Apex）交易对: %s", cfg.ApexSymbol)
	log.Printf("B所（Bybit）交易对: %s", cfg.BybitSymbol)
	log.Printf("运行模式: %d", cfg.Mode)

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	switch cfg.Mode {
	case 2:
		// 模型二：跨交易所联动套利 + 做市商被动抬价
		log.Println("=== 启动模型二：跨交易所联动套利 + 做市商被动抬价 ===")
		engine, err := strategy.NewModel2Engine(cfg)
		if err != nil {
			log.Fatalf("初始化模型二引擎失败: %v", err)
		}
		if err := engine.Start(); err != nil {
			log.Fatalf("启动模型二引擎失败: %v", err)
		}
		<-quit
		log.Println("收到退出信号，正在停止模型二引擎...")
		engine.Stop()

	default:
		// 模型一：被动价差套利（默认）
		log.Println("=== 启动模型一：被动价差套利 ===")
		engine, err := strategy.NewArbEngine(cfg)
		if err != nil {
			log.Fatalf("初始化套利引擎失败: %v", err)
		}
		if err := engine.Start(); err != nil {
			log.Fatalf("启动套利引擎失败: %v", err)
		}
		<-quit
		log.Println("收到退出信号，正在停止套利引擎...")
		engine.Stop()
	}

	log.Println("程序已安全退出")
}
