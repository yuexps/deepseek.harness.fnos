package main

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

var (
	cachedAppRemoteVer  string
	cachedAppCheckMutex sync.Mutex
)

// getAppVersion 获取当前应用版本号
func getAppVersion() string {
	return globalAppVer
}

// getAppRemoteVersion 获取远端最新版本号
func getAppRemoteVersion() string {
	cachedAppCheckMutex.Lock()
	defer cachedAppCheckMutex.Unlock()
	return cachedAppRemoteVer
}

// checkAppHasUpdate 检查是否存在新版本
func checkAppHasUpdate(localVer string) bool {
	cleanLocal := strings.TrimPrefix(strings.TrimSpace(localVer), "v")
	if cleanLocal == "" {
		return false
	}

	cachedAppCheckMutex.Lock()
	defer cachedAppCheckMutex.Unlock()
	if cachedAppRemoteVer == "" {
		return false
	}
	return CompareSemver(cachedAppRemoteVer, cleanLocal) > 0
}

// fetchAppRemoteUpdate 拉取远端最新版本
func fetchAppRemoteUpdate() {
	localVer := getAppVersion()
	if localVer == "" {
		return
	}

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	cfg := GetConfig()
	if cfg.NetworkProxy != "" {
		if pURL, err := url.Parse(cfg.NetworkProxy); err == nil {
			transport.Proxy = http.ProxyURL(pURL)
		}
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest("GET", "https://github.com/yuexps/deepseek.harness.fnos/releases/latest", nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "DeepSeek-Harness-FNOS")

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusTemporaryRedirect {
		loc := resp.Header.Get("Location")
		tag := path.Base(loc)
		rVer := strings.TrimPrefix(strings.TrimSpace(tag), "v")
		if rVer != "" && rVer != "latest" {
			cachedAppCheckMutex.Lock()
			oldRemoteVer := cachedAppRemoteVer
			cachedAppRemoteVer = rVer
			cachedAppCheckMutex.Unlock()

			cleanLocal := strings.TrimPrefix(strings.TrimSpace(localVer), "v")
			if CompareSemver(rVer, cleanLocal) > 0 && rVer != oldRemoteVer {
				LogInfo("[版本检测] 发现新版本 v%s (当前版本 v%s)", rVer, cleanLocal)
			}
			return
		}
	}
}

// StartAppUpdateChecker 启动后台版本检查轮询
func StartAppUpdateChecker() {
	go func() {
		time.Sleep(3 * time.Second)
		fetchAppRemoteUpdate()

		ticker := time.NewTicker(3 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			fetchAppRemoteUpdate()
		}
	}()
}
