package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

func InitHarness(pkgVar, appdest string) {
	pkgVarDir = pkgVar
	srcDir = filepath.Join(pkgVar, "src", "deepseek-harness")
	appDest = appdest

	// 全局配置 safe.directory，避免所有权异常导致 git 操作被拦截
	configCmd := exec.Command(gitBin, "config", "--global", "--add", "safe.directory", "*")
	_ = configCmd.Run()

	KillHarness()
	StartWatchdog()

	tarPath := filepath.Join(appDest, "deepseek-harness.tar.gz")
	zipVer := readAppDestVersion()

	// 1. 若 srcDir 不存在：初次安装解压/克隆
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		state.SetStatus(StatusBuilding, "正在准备初始化...")
		go func() {
			if _, err := os.Stat(tarPath); err == nil {
				pkgName := pkgTypeName()
				state.SetStatus(StatusBuilding, fmt.Sprintf("正在解压%s...", pkgName))
				LogInfo("解压%s: %s (版本: %s)", pkgName, tarPath, zipVer)
				if err := extractTarGz(tarPath, filepath.Dir(srcDir)); err != nil {
					LogWarning("解压%s失败: %s", pkgName, err)
					state.SetStatus(StatusStopped, "解压失败，请点击【更新构建】重试")
					return
				}
				_ = os.Remove(tarPath)
			} else {
				state.SetStatus(StatusBuilding, "正在克隆源码...")
				LogInfo("Git 克隆源码: %s", repoURL)
				if err := gitClone(); err != nil {
					LogWarning("Git 克隆失败: %s", err)
					state.SetStatus(StatusStopped, "克隆失败，请检查网络后点击【更新构建】")
					return
				}
			}

			if err := buildSource(true); err != nil {
				LogWarning("源码构建失败: %s", err)
				state.SetStatus(StatusStopped, "构建失败，请点击【更新构建】重试")
				return
			}
			refreshCommit()
			state.SetStatus(StatusStopped, "")
			LogInfo("初始化安装完成，正在启动服务")
			if err := Start(); err != nil {
				LogWarning("服务启动失败: %s", err)
			}
		}()
		return
	}

	// 2. 若 srcDir 已存在：检查内置源码包版本是否高于当前版本
	if _, err := os.Stat(tarPath); err == nil {
		installedVer := readVersion()
		if zipVer != "" && compareSemver(zipVer, installedVer) > 0 {
			pkgName := pkgTypeName()
			state.SetStatus(StatusBuilding, fmt.Sprintf("检测到新版本%s，正在准备更新...", pkgName))
			go func() {
				state.SetStatus(StatusBuilding, fmt.Sprintf("正在增量替换%s (%s → %s)...", pkgName, installedVer, zipVer))
				LogInfo("检测到%s版本更新 (%s → %s)，开始增量解压替换", pkgName, installedVer, zipVer)
				if err := extractTarGz(tarPath, filepath.Dir(srcDir)); err != nil {
					LogWarning("增量解压替换%s失败: %s", pkgName, err)
					state.SetStatus(StatusStopped, "解压更新失败，请点击【强制重建】重试")
					return
				}
				_ = os.Remove(tarPath)

				if err := buildSource(true); err != nil {
					LogWarning("增量更新后源码构建失败: %s", err)
					state.SetStatus(StatusStopped, "构建失败，请点击【强制重建】重试")
					return
				}
				refreshCommit()
				state.SetStatus(StatusStopped, "")
				LogInfo("增量版本更新完成，正在启动服务")
				if err := Start(); err != nil {
					LogWarning("服务启动失败: %s", err)
				}
			}()
			return
		}

		// 压缩包版本不高于已安装版本，直接清理
		pkgName := pkgTypeName()
		_ = os.Remove(tarPath)
		LogInfo("%s版本 (%s) 不高于当前版本 (%s)，清理并跳过解压", pkgName, zipVer, installedVer)
	}

	refreshCommit()
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
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return fmt.Errorf("源码不存在，请先点击【拉取更新】进行初始化")
	}
	if _, err := os.Stat(filepath.Join(srcDir, "node_modules")); os.IsNotExist(err) {
		return fmt.Errorf("依赖未安装，请先点击【强制重建】进行构建")
	}

	return startLocked()
}

// dshCliCmd 优先直接执行编译后的 CLI 入口，其次解析 package.json，失败时以 pnpm 兜底
func dshCliCmd(subArgs ...string) (string, []string) {
	cliBinJs := filepath.Join(srcDir, "apps", "cli", "lib", "bin.js")
	if _, err := os.Stat(cliBinJs); err == nil {
		return nodeBin(), append([]string{cliBinJs}, subArgs...)
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
					return nodeBin(), append(cmdArgs, subArgs...)
				}
			}
		}
	}
	return pnpmBin(), append([]string{"dsh"}, subArgs...)
}

func startLocked() error {
	killHarnessLocked()
	ClearAllPluginFailures()

	cfg := GetConfig()
	port := cfg.ServerPort
	if port <= 0 {
		port = 2298
	}

	bin, args := dshCliCmd("web", "--port", fmt.Sprintf("%d", port))
	cmd := exec.Command(bin, args...)
	cmd.Dir = srcDir
	cmd.Stdout = NewLogWriterInfo()
	cmd.Stderr = NewLogWriterWarn()
	setProcessGroup(cmd)

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
