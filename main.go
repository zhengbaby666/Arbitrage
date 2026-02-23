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

	// 初始化套利引擎
	engine, err := strategy.NewArbEngine(cfg)
	if err != nil {
		log.Fatalf("初始化套利引擎失败: %v", err)
	}

	// 启动套利
	if err := engine.Start(); err != nil {
		log.Fatalf("启动套利引擎失败: %v", err)
	}

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("收到退出信号，正在停止套利引擎...")
	engine.Stop()
	log.Println("套利引擎已安全停止")
}
