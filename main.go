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
	globalPkgVar string
	globalAppVer string
)

func main() {
	pkgVar := os.Getenv("DATA_LIBRARY_PATH")
	if pkgVar == "" {
		LogFatal("环境变量缺失: DATA_LIBRARY_PATH")
	}
	globalPkgVar = pkgVar

	appdest := os.Getenv("TRIM_APPDEST")
	if appdest == "" {
		LogFatal("环境变量缺失: TRIM_APPDEST")
	}

	appVer := os.Getenv("TRIM_APPVER")
	if appVer == "" {
		LogFatal("环境变量缺失: TRIM_APPVER")
	}
	globalAppVer = strings.TrimSpace(appVer)

	InitLogger(pkgVar)
	runUser := os.Getenv("DSH_RUN_USER")
	if runUser == "" {
		runUser = "root"
	}
	LogInfo("DeepSeek Harness 服务初始化启动 (DATA_LIBRARY_PATH=%s, TRIM_APPDEST=%s, TRIM_APPVER=%s, DSH_RUN_USER=%s)", pkgVar, appdest, globalAppVer, runUser)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		LogInfo("收到系统信号 %s，正在优雅退出", sig)
		stopAndWait()
		os.Exit(0)
	}()

	InitConfig(pkgVar)
	InitAppEnv(pkgVar)
	InitHarness(pkgVar, appdest)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	WebFS = embeddedWebFS
	InitRoutes(r)
	StartWorkspaceWatch()
	StartAppUpdateChecker()

	_ = os.MkdirAll(appdest, 0755)
	socketPath := filepath.Join(appdest, "web.sock")
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
