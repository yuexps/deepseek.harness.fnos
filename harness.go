package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	StatusStopped      = "stopped"
	StatusStarting     = "starting"
	StatusRunning      = "running"
	StatusBuilding     = "building"
	StatusSnapshotting = "snapshotting"
)

type HarnessState struct {
	mu            sync.RWMutex
	status        string
	targetVersion string
	startTime     time.Time
	lastMessage   string
	stateSubs     map[chan struct{}]struct{}
}

var state = &HarnessState{status: StatusStopped, stateSubs: make(map[chan struct{}]struct{})}

func (s *HarnessState) SetStatus(status, msg string) {
	s.mu.Lock()
	oldStatus := s.status
	oldMsg := s.lastMessage
	becameRunning := status == StatusRunning && s.status != status
	changed := s.status != status || s.lastMessage != msg
	s.status = status
	s.lastMessage = msg
	if status != StatusBuilding {
		s.targetVersion = ""
	}
	if becameRunning {
		s.startTime = time.Now()
	}
	s.mu.Unlock()
	if changed {
		if oldStatus != status || (msg != "" && msg != oldMsg) {
			if msg != "" {
				LogInfo("[状态变更] %s → %s: %s", oldStatus, status, msg)
			} else {
				LogInfo("[状态变更] %s → %s", oldStatus, status)
			}
		}
		s.notify()
	}
}

func (s *HarnessState) SetTargetVersion(tv string) {
	s.mu.Lock()
	if s.targetVersion != tv {
		s.targetVersion = tv
		s.mu.Unlock()
		s.notify()
		return
	}
	s.mu.Unlock()
}

func (s *HarnessState) Status() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *HarnessState) Snapshot() (status, uptime, lastMsg, version, buildTime, targetVersion string, startedAt int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status = s.status
	lastMsg = s.lastMessage
	targetVersion = s.targetVersion
	version = GetVersion()
	if status == StatusRunning && !s.startTime.IsZero() {
		startedAt = s.startTime.Unix()
		uptime = formatDuration(time.Since(s.startTime))
	}
	buildTime = GetBuildTime()
	return
}

func (s *HarnessState) notify() {
	s.mu.RLock()
	subs := make([]chan struct{}, 0, len(s.stateSubs))
	for ch := range s.stateSubs {
		subs = append(subs, ch)
	}
	s.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *HarnessState) SubscribeState(buf int) (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan struct{}, buf)
	s.stateSubs[ch] = struct{}{}
	return ch, func() {
		s.mu.Lock()
		delete(s.stateSubs, ch)
		s.mu.Unlock()
	}
}

func (s *HarnessState) Poke() {
	s.notify()
}

// isRuntimeReady 校验 NPM 运行环境与核心入口是否就绪
func isRuntimeReady() bool {
	if fi, err := os.Stat(runtimeDir); err != nil || !fi.IsDir() {
		return false
	}
	cliBin := filepath.Join(runtimeDir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if fi, err := os.Stat(cliBin); err == nil && fi.Size() > 0 {
		return true
	}
	return false
}

// EvaluateDeploymentPolicy 判定是否需要部署或升级内置离线包
func EvaluateDeploymentPolicy(tarPath string) (shouldDeploy bool, isUpgrade bool, reason string) {
	if _, err := os.Stat(tarPath); err != nil {
		return false, false, "内置离线包不存在"
	}

	zipVer := readAppDestVersion()
	installedVer := readVersion()

	// 运行环境未就绪时执行初始化或自愈部署
	if !isRuntimeReady() {
		return true, false, fmt.Sprintf("运行环境未就绪，正在部署内置离线包 (v%s)...", zipVer)
	}

	// 仅在安装包版本高于本地运行版本时执行升级
	if zipVer != "" && installedVer != "" && CompareSemver(zipVer, installedVer) > 0 {
		return true, true, fmt.Sprintf("检测到新版本安装包 (v%s → v%s)，正在升级部署...", installedVer, zipVer)
	}

	// 默认保留本地运行环境与在线更新
	if installedVer != "" {
		return false, false, fmt.Sprintf("本地运行版本 (v%s) 已就绪，跳过离线包解压", installedVer)
	}
	return false, false, "本地运行环境已就绪，跳过离线包解压"
}

// deployBuiltinPackage 部署安装包内置离线包并拉起服务
func deployBuiltinPackage(tarPath, zipVer string, isUpgrade bool) {
	state.SetStatus(StatusBuilding, "正在准备部署运行环境...")
	go func() {
		installedVer := readVersion()
		if isUpgrade && installedVer != "" && zipVer != "" {
			state.SetStatus(StatusBuilding, fmt.Sprintf("正在升级运行环境 (v%s → v%s)...", installedVer, zipVer))
			LogInfo("检测到新版离线包 (v%s → v%s)，开始部署", installedVer, zipVer)
		} else {
			state.SetStatus(StatusBuilding, "正在部署内置运行环境...")
			LogInfo("部署内置离线包 (v%s)", zipVer)
		}

		_ = safeRemoveAll(runtimeDir)

		if err := extractTarGz(tarPath, runtimeDir); err != nil {
			LogWarning("解压离线包失败: %s", err)
			state.SetStatus(StatusStopped, "解压离线包失败: "+err.Error())
			return
		}

		refreshVersion()
		SetBuildTime(time.Now())
		go installPnpm()
		state.SetStatus(StatusStopped, "")
		LogInfo("运行环境部署完成，正在启动服务")
		if err := Start(); err != nil {
			LogWarning("服务启动失败: %s", err)
		}
	}()
}

// migrateFromGitToNpm 检测并清理旧版 Git 源码环境
func migrateFromGitToNpm() {
	legacySrcDir := filepath.Join(globalPkgVar, "src")
	if fi, err := os.Stat(legacySrcDir); err == nil && fi.IsDir() {
		LogInfo("检测到旧版源码目录，正在自动清理: %s", legacySrcDir)
		if err := safeRemoveAll(legacySrcDir); err != nil {
			LogWarning("清理旧版源码目录失败: %s", err)
		} else {
			LogInfo("旧版源码目录清理完成")
		}
	}

	// 清理旧版本可能残留的构建锁文件
	legacyFiles := []string{
		filepath.Join(globalPkgVar, "building.lock"),
		filepath.Join(globalPkgVar, "git.lock"),
	}
	for _, f := range legacyFiles {
		if _, err := os.Stat(f); err == nil {
			_ = os.Remove(f)
		}
	}
}

func InitHarness() {
	KillHarness()
	StartWatchdog()
	StartUsageSampler()
	migrateFromGitToNpm()

	tarPath := filepath.Join(globalAppDest, "deepseek-harness.tar.gz")
	zipVer := readAppDestVersion()

	// 评估内置离线包部署决策
	if _, err := os.Stat(tarPath); err == nil {
		shouldDeploy, isUpgrade, reason := EvaluateDeploymentPolicy(tarPath)
		if shouldDeploy {
			LogInfo("%s", reason)
			deployBuiltinPackage(tarPath, zipVer, isUpgrade)
			return
		}
		LogInfo("%s", reason)
	} else if !isRuntimeReady() {
		// 未内置压缩包且本地无运行时，通过 NPM 自动安装部署
		state.SetStatus(StatusBuilding, "正在通过 NPM 安装运行环境...")
		LogInfo("未检测到离线包，通过 NPM 安装核心服务: %s", dshPackageName)
		go func() {
			_ = safeRemoveAll(runtimeDir)
			if err := installDshFromNpm(""); err != nil {
				LogWarning("NPM 安装核心服务失败: %s", err)
				state.SetStatus(StatusStopped, "安装失败: "+err.Error())
				return
			}
			refreshVersion()
			state.SetStatus(StatusStopped, "")
			LogInfo("NPM 运行时部署完成，正在启动服务")
			if err := Start(); err != nil {
				LogWarning("服务启动失败: %s", err)
			}
		}()
		return
	}

	// 常规启动并按上次状态自启
	refreshVersion()
	ApplyBuiltinSkillConfig()
	go installPnpm()
	if GetLastRunState() == StatusRunning {
		LogInfo("检测到上次运行状态为 running，正在自动拉起服务")
		go func() {
			if err := Start(); err != nil {
				LogWarning("服务启动失败: %s", err)
			}
		}()
	} else {
		LogInfo("上次运行状态非 running (%s)，跳过自动启动", GetLastRunState())
	}
}

func Start() error {
	procMu.Lock()
	defer procMu.Unlock()

	if state.Status() == StatusBuilding {
		return fmt.Errorf("正在部署更新中，请稍候再试")
	}
	if state.Status() == StatusSnapshotting {
		return fmt.Errorf("正在执行快照维护，请稍候再试")
	}
	if state.Status() == StatusStarting {
		return fmt.Errorf("服务正在启动中，请稍候")
	}
	if state.Status() == StatusRunning {
		return fmt.Errorf("服务已在运行中")
	}
	if !isRuntimeReady() {
		return fmt.Errorf("运行环境未就绪或核心依赖缺失")
	}

	return startLocked()
}

// dshCliCmd 构造 DSH CLI 执行命令与参数
func dshCliCmd(subArgs ...string) (string, []string) {
	cfg := GetConfig()
	var v8Args []string
	if cfg.HeapMemoryLimit > 0 {
		v8Args = append(v8Args, fmt.Sprintf("--max-old-space-size=%d", cfg.HeapMemoryLimit*1024))
	}

	cliBinJs := filepath.Join(runtimeDir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if _, err := os.Stat(cliBinJs); err == nil {
		return nodeBin(), append(append(v8Args, cliBinJs), subArgs...)
	}

	binLink := filepath.Join(runtimeDir, "node_modules", ".bin", "dsh")
	if _, err := os.Stat(binLink); err == nil {
		return binLink, subArgs
	}

	return nodeBin(), append(append(v8Args, cliBinJs), subArgs...)
}

var (
	launchTokenMu      sync.RWMutex
	currentLaunchToken string
	launchTokenRe      = regexp.MustCompile(`dsh web: .*?[?&]token=([A-Za-z0-9_-]+)`)

	dshSessionMu      sync.RWMutex
	cachedDshCookie   string
	dshSessionLastTry time.Time
)

func GetCurrentLaunchToken() string {
	launchTokenMu.RLock()
	defer launchTokenMu.RUnlock()
	return currentLaunchToken
}

func SetCurrentLaunchToken(token string) {
	trimmed := strings.TrimSpace(token)
	launchTokenMu.Lock()
	if currentLaunchToken != trimmed {
		currentLaunchToken = trimmed
		InvalidateDshSession()
	}
	launchTokenMu.Unlock()
	if trimmed != "" {
		display := trimmed
		if len(display) > 8 {
			display = display[:8] + "..."
		}
		LogInfo("已捕获 Web 会话令牌: %s", display)
	}
}

// InvalidateDshSession 清空缓存的官方会话凭据
func InvalidateDshSession() {
	dshSessionMu.Lock()
	cachedDshCookie = ""
	dshSessionLastTry = time.Time{}
	dshSessionMu.Unlock()
}

// GetDshSessionCookie 获取官方会话凭据，缺失时自动向本地服务换取
func GetDshSessionCookie() string {
	dshSessionMu.RLock()
	if cachedDshCookie != "" {
		c := cachedDshCookie
		dshSessionMu.RUnlock()
		return c
	}
	if !dshSessionLastTry.IsZero() && time.Since(dshSessionLastTry) < 2*time.Second {
		dshSessionMu.RUnlock()
		return ""
	}
	dshSessionMu.RUnlock()

	token := GetCurrentLaunchToken()
	if token == "" {
		return ""
	}
	return exchangeDshSessionCookie(token)
}

// exchangeDshSessionCookie 使用启动令牌向本地服务换取会话凭据
func exchangeDshSessionCookie(token string) string {
	dshSessionMu.Lock()
	dshSessionLastTry = time.Now()
	dshSessionMu.Unlock()
	port := GetConfig().GetServerPort()
	authority := fmt.Sprintf("127.0.0.1:%d", port)
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/?token=%s", authority, url.QueryEscape(token)), nil)
	if err != nil {
		return ""
	}
	req.Host = authority
	req.Header.Set("Host", authority)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	_ = resp.Body.Close()

	for _, ck := range resp.Cookies() {
		if strings.HasPrefix(ck.Name, "dsh-auth-") && ck.Value != "" {
			val := ck.Name + "=" + ck.Value
			dshSessionMu.Lock()
			cachedDshCookie = val
			dshSessionMu.Unlock()
			LogInfo("已换取官方 Web 会话凭据 (authority=%s)", authority)
			return val
		}
	}
	return ""
}

// tokenCaptureWriter 在向日志系统输出的同时实时提取 Launch Token
type tokenCaptureWriter struct {
	inner  *LineLogWriter
	mu     sync.Mutex
	buf    bytes.Buffer
	onLine func(line string)
}

func (w *tokenCaptureWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	w.mu.Lock()
	w.buf.Write(p)
	for {
		b := w.buf.Bytes()
		idx := bytes.IndexByte(b, '\n')
		if idx < 0 {
			break
		}
		line := string(b[:idx])
		w.onLine(line)
		w.buf.Next(idx + 1)
	}
	w.mu.Unlock()
	return n, err
}

func startLocked() error {
	killHarnessLocked()
	SetCurrentLaunchToken("")

	// 保护敏感凭据文件仅属主可读写 (mode 600)
	credFile := filepath.Join(globalDshHome, ".credentials.yaml")
	if _, err := os.Stat(credFile); err == nil {
		_ = os.Chmod(credFile, 0600)
	}

	cfg := GetConfig()
	port := cfg.GetServerPort()

	bin, args := dshCliCmd("web", "--port", fmt.Sprintf("%d", port), "--no-open")
	cmd := exec.Command(bin, args...)
	cmd.Dir = runtimeDir
	cmd.Stdout = &tokenCaptureWriter{
		inner: NewLogWriterInfo(),
		onLine: func(line string) {
			if m := launchTokenRe.FindStringSubmatch(line); len(m) > 1 {
				SetCurrentLaunchToken(m[1])
			}
		},
	}
	cmd.Stderr = NewLogWriterWarn()
	setProcessGroup(cmd)

	ApplyProxyEnv()
	cmdEnv := append([]string{}, os.Environ()...)
	if cfg.HeapMemoryLimit > 0 {
		nodeOpt := fmt.Sprintf("--max-old-space-size=%d", cfg.HeapMemoryLimit*1024)
		if existingOpt := os.Getenv("NODE_OPTIONS"); existingOpt != "" {
			nodeOpt = existingOpt + " " + nodeOpt
		}
		cmdEnv = append(cmdEnv, "NODE_OPTIONS="+nodeOpt)
	}
	cmd.Env = cmdEnv

	if err := cmd.Start(); err != nil {
		state.SetStatus(StatusStopped, "启动失败: "+err.Error())
		return err
	}

	mp := &managedProcess{cmd: cmd, done: make(chan struct{})}
	process = mp

	_ = os.WriteFile(pidFilePath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0644)

	state.SetStatus(StatusStarting, "服务主进程已拉起，正在等待 Web 服务就绪...")
	LogInfo("服务主进程已拉起 (PID=%d)，正在等待 Web 服务就绪...", cmd.Process.Pid)

	go waitAndActivateReverseProxy(mp, port)

	go func(mp *managedProcess) {
		err := mp.cmd.Wait()

		procMu.Lock()
		if mp.stopRequested {
			if process == mp {
				process = nil
				SetCurrentLaunchToken("")
				removePidFileIfMatches(mp.Pid())
				stopReverseProxy()
				LogInfo("服务主进程已按要求停止 (PID=%d)", mp.Pid())
				state.SetStatus(StatusStopped, "")
			}
			procMu.Unlock()
			mp.closeDone()
			return
		}
		procMu.Unlock()

		// 非主动退出由看门狗接管
		if err != nil {
			LogWarning("服务主进程异常退出 (PID=%d): %s", mp.Pid(), err)
		} else {
			LogInfo("服务主进程退出 (PID=%d)", mp.Pid())
		}
	}(mp)

	return nil
}

func stopAndWait() {
	stopReverseProxy()

	procMu.Lock()
	mp := process
	var pid int
	if mp != nil {
		mp.stopRequested = true
		pid = mp.Pid()
		LogInfo("终止服务主进程 (PID=%d)", pid)
		killProcessTree(pid)
		killProcessGroup(pid)
		removePidFileIfMatches(pid)
	}
	procMu.Unlock()

	if mp != nil {
		// 等待目标进程消亡
		deadline := time.Now().Add(1 * time.Second)
		for time.Now().Before(deadline) {
			if pid > 0 && !isProcessAlive(pid) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		mp.closeDone()

		procMu.Lock()
		if process == mp {
			process = nil
			SetCurrentLaunchToken("")
		}
		procMu.Unlock()
		state.SetStatus(StatusStopped, "")
	}
}

func Stop() error {
	stopAndWait()
	return nil
}

func Restart() error {
	stopAndWait()
	return Start()
}

func restartService() {
	LogInfo("部署完成，正在重启服务")
	stopAndWait()
	state.SetStatus(StatusStopped, "")
	if err := Start(); err != nil {
		LogWarning("服务重启失败: %s", err)
		state.SetStatus(StatusStopped, "启动失败: "+err.Error())
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d小时%d分%d秒", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%d分%d秒", m, s)
	}
	return fmt.Sprintf("%d秒", s)
}

func waitAndActivateReverseProxy(mp *managedProcess, port int) {
	targetURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.After(300 * time.Second)

	for {
		select {
		case <-mp.done:
			// 进程已退出，终止探测
			return
		case <-timeout:
			LogWarning("Web 服务就绪探测超时 (300s)，目标端口: %d", port)
			procMu.Lock()
			if process == mp {
				killHarnessLocked()
				state.SetStatus(StatusStopped, fmt.Sprintf("Web 服务就绪探测超时 (端口 %d 未响应)", port))
			}
			procMu.Unlock()
			return
		case <-ticker.C:
			// 优先尝试 HTTP 请求获取响应
			resp, err := client.Get(targetURL)
			ready := false
			if err == nil {
				_ = resp.Body.Close()
				ready = true
			} else {
				// 兜底尝试 TCP 握手是否已开放监听
				conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
				if dialErr == nil {
					_ = conn.Close()
					ready = true
				}
			}

			if ready {
				procMu.Lock()
				if process == mp && !mp.stopRequested && isProcessAlive(mp.Pid()) {
					startReverseProxy()
					state.SetStatus(StatusRunning, "")
					SetLastRunState(StatusRunning)
				}
				procMu.Unlock()
				return
			}
		}
	}
}
