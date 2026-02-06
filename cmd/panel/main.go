package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/AliceNetworks/gost-panel/internal/api"
	"github.com/AliceNetworks/gost-panel/internal/config"
	"github.com/AliceNetworks/gost-panel/internal/model"
	"github.com/AliceNetworks/gost-panel/internal/service"
)

var (
	listenAddr  = flag.String("listen", "", "Listen address (e.g., :8080, 0.0.0.0:8080)")
	dbPath      = flag.String("db", "", "Database path")
	debug       = flag.Bool("debug", false, "Enable debug mode")
	showVersion = flag.Bool("version", false, "Show version")
	showHelp    = flag.Bool("help", false, "Show help")
)

func main() {
	flag.Parse()

	if *showHelp {
		printUsage()
		os.Exit(0)
	}

	if *showVersion {
		fmt.Printf("GOST Panel %s\n", api.CurrentAgentVersion)
		fmt.Printf("Build Time: %s\n", api.AgentBuildTime)
		os.Exit(0)
	}

	// 加载配置
	cfg := config.Load()

	// 命令行参数覆盖环境变量
	if *listenAddr != "" {
		cfg.ListenAddr = *listenAddr
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *debug {
		cfg.Debug = true
	}

	// 初始化数据库
	db, err := model.InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}

	// 初始化服务
	svc := service.NewService(db, cfg)

	// 启动流量历史记录定时任务
	go startTrafficRecorder(svc)

	// 启动 API 服务
	server := api.NewServer(svc, cfg)

	log.Printf("GOST Panel starting on %s", cfg.ListenAddr)
	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("GOST Panel - GOST Proxy Management Panel")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  gost-panel [options]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -listen string    Listen address (default \":8080\")")
	fmt.Println("                    Examples: :8080, 0.0.0.0:8080, 127.0.0.1:8080")
	fmt.Println("  -db string        Database path (default \"./data/panel.db\")")
	fmt.Println("  -debug            Enable debug mode")
	fmt.Println("  -version          Show version")
	fmt.Println("  -help             Show this help")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  LISTEN_ADDR       Listen address (same as -listen)")
	fmt.Println("  DB_PATH           Database path (same as -db)")
	fmt.Println("  JWT_SECRET        JWT secret key (required for production)")
	fmt.Println("  DEBUG             Enable debug mode (true/false)")
	fmt.Println("  ALLOWED_ORIGINS   Comma-separated list of allowed CORS origins")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  gost-panel -listen :9000")
	fmt.Println("  gost-panel -listen 0.0.0.0:8080 -db /var/lib/gost-panel/panel.db")
	fmt.Println("  LISTEN_ADDR=:9000 JWT_SECRET=mysecret gost-panel")
}

// startTrafficRecorder 启动流量记录定时任务
func startTrafficRecorder(svc *service.Service) {
	// 每分钟记录一次流量数据
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// 立即记录一次
	svc.RecordTrafficHistory()

	for range ticker.C {
		if err := svc.RecordTrafficHistory(); err != nil {
			log.Printf("Failed to record traffic history: %v", err)
		}
	}
}
