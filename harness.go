package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
	StatusStopped  = "stopped"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusBuilding = "building"
)

type HarnessState struct {
	mu           sync.RWMutex
	status       string
	targetCommit string
	startTime    time.Time
	lastMessage  string
	stateSubs    map[chan struct{}]struct{}
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
		s.targetCommit = ""
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

func (s *HarnessState) SetTargetCommit(tc string) {
	s.mu.Lock()
	if s.targetCommit != tc {
		s.targetCommit = tc
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

func (s *HarnessState) Snapshot() (status, uptime, lastMsg, commit, version, buildTime, targetCommit string, startedAt int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status = s.status
	lastMsg = s.lastMessage
	targetCommit = s.targetCommit
	commit = GetCommit()
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

var (
	srcDir    string
	appDest   string
	pkgVarDir string
)

// isRuntimeReady 校验工作区产物与依赖是否完备
func isRuntimeReady() bool {
	if fi, err := os.Stat(srcDir); err != nil || !fi.IsDir() {
		return false
	}
	if fi, err := os.Stat(filepath.Join(srcDir, "package.json")); err != nil || fi.IsDir() {
		return false
	}
	cliBin := filepath.Join(srcDir, "apps", "cli", "lib", "bin.js")
	fi, err := os.Stat(cliBin)
	if err != nil || fi.Size() == 0 {
		return false
	}
	if fi, err := os.Stat(filepath.Join(srcDir, "node_modules")); err != nil || !fi.IsDir() {
		return false
	}
	return true
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
		return true, false, fmt.Sprintf("运行环境未就绪或产物缺失，正在部署预构建包 (v%s)...", zipVer)
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

// deployPrebuilt 部署内置离线包并拉起服务
func deployPrebuilt(tarPath, zipVer string, isUpgrade bool) {
	state.SetStatus(StatusBuilding, "正在准备部署预构建包...")
	go func() {
		installedVer := readVersion()
		if isUpgrade && installedVer != "" && zipVer != "" {
			state.SetStatus(StatusBuilding, fmt.Sprintf("正在升级部署预构建包 (v%s → v%s)...", installedVer, zipVer))
			LogInfo("检测到新版本预构建包 (v%s → v%s)，正在安全部署: %s", installedVer, zipVer, tarPath)
		} else {
			state.SetStatus(StatusBuilding, "正在解压部署内置预构建包...")
			LogInfo("解压部署预构建包: %s (版本: v%s)", tarPath, zipVer)
		}

		_ = safeRemoveAll(srcDir)

		if err := extractTarGz(tarPath, filepath.Dir(srcDir)); err != nil {
			LogWarning("解压部署预构建包失败: %s", err)
			state.SetStatus(StatusStopped, "解压离线安装包失败: "+err.Error())
			return
		}

		if err := installPnpm(); err != nil {
			LogWarning("初始化 pnpm 运行环境失败: %s", err)
		}

		refreshCommit()
		SetBuildTime(time.Now())
		state.SetStatus(StatusStopped, "")
		LogInfo("预构建包部署就绪，正在启动服务")
		if err := Start(); err != nil {
			LogWarning("服务启动失败: %s", err)
		}
	}()
}

func InitHarness(pkgVar, appdest string) {
	pkgVarDir = pkgVar
	srcDir = filepath.Join(pkgVar, "src", "deepseek-harness")
	appDest = appdest

	KillHarness()
	StartWatchdog()

	tarPath := filepath.Join(appDest, "deepseek-harness.tar.gz")
	zipVer := readAppDestVersion()

	// 评估预构建包部署决策
	if _, err := os.Stat(tarPath); err == nil {
		shouldDeploy, isUpgrade, reason := EvaluateDeploymentPolicy(tarPath)
		if shouldDeploy {
			LogInfo("%s", reason)
			deployPrebuilt(tarPath, zipVer, isUpgrade)
			return
		}
		LogInfo("%s", reason)
	} else if !isSourceValid() {
		// 未内置压缩包且本地无源码时，通过 Git 克隆源码并编译启动
		state.SetStatus(StatusBuilding, "未检测到内置预构建包，正在从远程克隆源码...")
		LogInfo("未检测到内置预构建包 (%s)，开始通过 Git 克隆源码: %s", tarPath, repoURL)
		go func() {
			_ = safeRemoveAll(srcDir)
			if err := gitClone(); err != nil {
				LogWarning("Git 克隆源码失败: %s", err)
				state.SetStatus(StatusStopped, formatGitError("克隆源码失败", err))
				return
			}
			if err := buildFromSource(false); err != nil {
				LogWarning("源码构建初始化失败: %s", err)
				state.SetStatus(StatusStopped, "构建失败: "+err.Error())
				return
			}
			refreshCommit()
			state.SetStatus(StatusStopped, "")
			LogInfo("源码克隆与构建完成，正在启动服务")
			if err := Start(); err != nil {
				LogWarning("服务启动失败: %s", err)
			}
		}()
		return
	}

	// 常规启动并按上次状态自启
	refreshCommit()
	ApplyBuiltinSkillConfig(pkgVar, appdest)
	go func() {
		_ = installPnpm()
	}()
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
		return fmt.Errorf("正在构建中，请稍候再试")
	}
	if state.Status() == StatusStarting {
		return fmt.Errorf("服务正在启动中，请稍候")
	}
	if state.Status() == StatusRunning {
		return fmt.Errorf("服务已在运行中")
	}
	if !isRuntimeReady() {
		return fmt.Errorf("运行环境未就绪或关键构建产物缺失")
	}

	return startLocked()
}

// dshCliCmd 优先直接执行编译后的 CLI 入口，其次解析 package.json，失败时以 pnpm 兜底
func dshCliCmd(subArgs ...string) (string, []string) {
	cfg := GetConfig()
	var v8Args []string
	if cfg.HeapMemoryLimit > 0 {
		v8Args = append(v8Args, fmt.Sprintf("--max-old-space-size=%d", cfg.HeapMemoryLimit*1024))
	}

	cliBinJs := filepath.Join(srcDir, "apps", "cli", "lib", "bin.js")
	if _, err := os.Stat(cliBinJs); err == nil {
		return nodeBin(), append(append(v8Args, cliBinJs), subArgs...)
	}

	pkgPath := filepath.Join(srcDir, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &pkg); err == nil {
			if script := strings.TrimSpace(pkg.Scripts["dsh"]); script != "" {
				parts := strings.Fields(script)
				if len(parts) > 0 && (parts[0] == "tsx" || parts[0] == "node") {
					cmdArgs := append([]string{}, parts[1:]...)
					return nodeBin(), append(append(v8Args, cmdArgs...), subArgs...)
				}
			}
		}
	}
	return pnpmBin(), append([]string{"dsh"}, subArgs...)
}

var (
	launchTokenMu      sync.RWMutex
	currentLaunchToken string
	launchTokenRe      = regexp.MustCompile(`dsh web: .*?[?&]token=([A-Za-z0-9_-]+)`)
)

func GetCurrentLaunchToken() string {
	launchTokenMu.RLock()
	defer launchTokenMu.RUnlock()
	return currentLaunchToken
}

func SetCurrentLaunchToken(token string) {
	launchTokenMu.Lock()
	currentLaunchToken = strings.TrimSpace(token)
	launchTokenMu.Unlock()
	if token != "" {
		display := token
		if len(display) > 8 {
			display = display[:8] + "..."
		}
		LogInfo("已捕获 Web 会话令牌: %s", display)
	}
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
	credFile := filepath.Join(pkgVarDir, "dsh-data", ".credentials.yaml")
	if _, err := os.Stat(credFile); err == nil {
		_ = os.Chmod(credFile, 0600)
	}

	cfg := GetConfig()
	port := cfg.ServerPort
	if port <= 0 {
		port = 2298
	}

	bin, args := dshCliCmd("web", "--port", fmt.Sprintf("%d", port), "--no-open")
	cmd := exec.Command(bin, args...)
	cmd.Dir = srcDir
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

	if cfg.HeapMemoryLimit > 0 {
		nodeOpt := fmt.Sprintf("--max-old-space-size=%d", cfg.HeapMemoryLimit*1024)
		if existingOpt := os.Getenv("NODE_OPTIONS"); existingOpt != "" {
			nodeOpt = existingOpt + " " + nodeOpt
		}
		cmd.Env = append(os.Environ(), "NODE_OPTIONS="+nodeOpt)
	}

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
		current := process
		if current == mp {
			process = nil
			SetCurrentLaunchToken("")
			removePidFileIfMatches(mp.cmd.Process.Pid)
			stopReverseProxy()

			if mp.stopRequested {
				LogInfo("服务主进程已按要求停止 (PID=%d)", mp.cmd.Process.Pid)
				state.SetStatus(StatusStopped, "")
			} else if err != nil {
				LogWarning("服务主进程异常退出 (PID=%d): %s", mp.cmd.Process.Pid, err)
				state.SetStatus(StatusStopped, "进程意外退出: "+err.Error())
			} else {
				LogInfo("服务主进程正常退出 (PID=%d)", mp.cmd.Process.Pid)
				state.SetStatus(StatusStopped, "")
			}
		} else {
			removePidFileIfMatches(mp.cmd.Process.Pid)
		}
		procMu.Unlock()

		close(mp.done)
	}(mp)

	return nil
}

func stopAndWait() {
	stopReverseProxy()

	procMu.Lock()
	mp := process
	if mp != nil {
		mp.stopRequested = true
		LogInfo("终止服务主进程 (PID=%d)", mp.cmd.Process.Pid)
		killProcessGroup(mp.cmd.Process.Pid)
		removePidFileIfMatches(mp.cmd.Process.Pid)
	}
	procMu.Unlock()

	if mp != nil {
		<-mp.done
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
	LogInfo("构建完成，正在重启服务")
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

	timeout := time.After(60 * time.Second)

	for {
		select {
		case <-mp.done:
			// 进程已退出，终止探测
			return
		case <-timeout:
			LogWarning("Web 服务就绪探测超时 (60s)，目标端口: %d", port)
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
				if process == mp && !mp.stopRequested && mp.cmd != nil && mp.cmd.Process != nil && isProcessAlive(mp.cmd.Process.Pid) {
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
