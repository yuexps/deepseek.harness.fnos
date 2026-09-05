package main

import (
	"embed"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
)

//go:embed frontend/dist/*
var embeddedWebFS embed.FS

var (
	globalPkgVar       string
	globalAppVer       string
	globalAppDest      string
	globalRunUser      string
	globalDshHome      string
	globalHomeDir      string
	globalPnpmDir      string
	globalPnpmHome     string
	globalNpmCache     string
	globalPluginsDir   string
	globalSnapshotsDir string
	srcDir             string
)

func main() {
	globalPkgVar = os.Getenv("DATA_LIBRARY_PATH")
	if globalPkgVar == "" {
		LogFatal("环境变量缺失: DATA_LIBRARY_PATH")
	}

	globalAppDest = strings.TrimSpace(os.Getenv("TRIM_APPDEST"))
	if globalAppDest == "" {
		LogFatal("环境变量缺失: TRIM_APPDEST")
	}

	globalAppVer = strings.TrimSpace(os.Getenv("TRIM_APPVER"))
	if globalAppVer == "" {
		LogFatal("环境变量缺失: TRIM_APPVER")
	}

	globalRunUser = os.Getenv("DSH_RUN_USER")
	if globalRunUser == "" {
		globalRunUser = "root"
	}

	globalDshHome = filepath.Join(globalPkgVar, "dsh-data")
	globalHomeDir = filepath.Join(globalPkgVar, "home")
	globalPnpmDir = filepath.Join(globalPkgVar, "pnpm-env")
	globalPnpmHome = filepath.Join(globalPkgVar, "pnpm-home")
	globalNpmCache = filepath.Join(globalPkgVar, "npm-cache")
	globalPluginsDir = filepath.Join(globalPkgVar, "plugins")
	globalSnapshotsDir = filepath.Join(globalPkgVar, "snapshots")
	srcDir = filepath.Join(globalPkgVar, "src", "deepseek-harness")

	InitLogger()
	LogInfo("DeepSeek Harness 服务初始化启动 (DATA_LIBRARY_PATH=%s, TRIM_APPDEST=%s, TRIM_APPVER=%s, DSH_RUN_USER=%s)", globalPkgVar, globalAppDest, globalAppVer, globalRunUser)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		LogInfo("收到系统信号 %s，正在优雅退出", sig)
		stopAndWait()
		os.Exit(0)
	}()

	InitConfig()
	InitAppEnv()
	InitHarness()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	WebFS = embeddedWebFS
	InitRoutes(r)
	StartWorkspaceWatch()
	StartAppUpdateChecker()

	_ = os.MkdirAll(globalAppDest, 0755)
	socketPath := filepath.Join(globalAppDest, "web.sock")
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		LogFatal("Unix Socket 监听失败 [%s]: %s", socketPath, err)
	}
	defer listener.Close()

	if err := os.Chmod(socketPath, 0666); err != nil {
		LogWarning("Unix Socket 权限设置失败: %s", err)
	}

	LogInfo("HTTP 服务已就绪，监听 Socket: %s", socketPath)
	if err := r.RunListener(listener); err != nil {
		LogFatal("HTTP 服务异常退出: %s", err)
	}
}
