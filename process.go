package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type managedProcess struct {
	cmd           *exec.Cmd
	adoptedPid    int
	stopRequested bool
	failCount     int
	done          chan struct{}
	doneOnce      sync.Once
}

func (mp *managedProcess) Pid() int {
	if mp == nil {
		return 0
	}
	if mp.adoptedPid > 0 {
		return mp.adoptedPid
	}
	if mp.cmd != nil && mp.cmd.Process != nil {
		return mp.cmd.Process.Pid
	}
	return 0
}

func (mp *managedProcess) closeDone() {
	if mp != nil {
		mp.doneOnce.Do(func() {
			close(mp.done)
		})
	}
}

var (
	procMu  sync.Mutex
	process *managedProcess
)

func pidFilePath() string {
	return filepath.Join(globalPkgVar, "harness.pid")
}

func removePidFileIfMatches(pid int) {
	if data, err := os.ReadFile(pidFilePath()); err == nil {
		if filePid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && filePid == pid {
			_ = os.Remove(pidFilePath())
		}
	}
}

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func killProcess(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func killProcessGroup(pgid int) bool {
	if pgid <= 0 {
		return true
	}
	// 向负数 PGID 发送信号，作用于整个进程组中所有遗留进程
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err == syscall.ESRCH {
		return true
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	return syscall.Kill(-pgid, 0) == syscall.ESRCH
}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	// 查询该进程所属的 PGID 尝试整组清理
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 {
		if killProcessGroup(pgid) {
			return
		}
	}
	killProcess(pid)
}

func findPidsOnPort(port int) []int {
	if port <= 0 {
		return nil
	}
	out, err := exec.Command("fuser", fmt.Sprintf("%d/tcp", port)).Output()
	if err != nil {
		return nil
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	var pids []int
	for _, p := range parts {
		if pid, err := strconv.Atoi(p); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// isDshProcess 检查进程是否属于 DSH 服务
func isDshProcess(pid int) bool {
	if pid <= 0 || srcDir == "" {
		return false
	}
	if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil && strings.HasPrefix(cwd, srcDir) {
		return true
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		cmdline := string(data)
		return strings.Contains(cmdline, srcDir) || strings.Contains(cmdline, "deepseek-harness")
	}
	return false
}

// findDshPidOnPort 获取指定端口上的首个 DSH 进程 PID
func findDshPidOnPort(port int) int {
	for _, pid := range findPidsOnPort(port) {
		if isProcessAlive(pid) && isDshProcess(pid) {
			return pid
		}
	}
	return 0
}

// GetActiveDshPid 获取当前存活运行的 DSH 进程 PID（优先端口活跃进程，其次 pid 文件）
func GetActiveDshPid(port int) int {
	if port > 0 {
		if pid := findDshPidOnPort(port); pid > 0 {
			return pid
		}
	}
	if data, err := os.ReadFile(pidFilePath()); err == nil {
		if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p > 0 && isProcessAlive(p) {
			return p
		}
	}
	return 0
}

// waitForPortFree 循环检测端口是否彻底释放（最多等待 500ms）
func waitForPortFree(port int) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(findPidsOnPort(port)) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// killHarnessLocked 彻底终止：清理进程组并深度排查端口残留
func killHarnessLocked() {
	// 清理内存记录的进程句柄
	if process != nil {
		pid := process.Pid()
		if pid > 0 {
			LogInfo("清理运行进程 (PID=%d)", pid)
			killProcessTree(pid)
			_ = killProcessGroup(pid)
			removePidFileIfMatches(pid)
		}
		process = nil
	}

	// 清理 PID 文件记录的进程组
	if data, err := os.ReadFile(pidFilePath()); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			LogInfo("清理残留进程组 (PGID=%d)", pid)
			_ = killProcessGroup(pid)
			killProcessTree(pid)
			removePidFileIfMatches(pid)
		}
	}

	// 清理端口上的 DSH 残留进程
	port := GetConfig().GetServerPort()
	for _, pid := range findPidsOnPort(port) {
		if isProcessAlive(pid) {
			if isDshProcess(pid) {
				LogInfo("终止端口 %d 残留进程 (PID=%d)", port, pid)
				killProcessTree(pid)
				_ = killProcessGroup(pid)
			} else {
				LogWarning("检测到端口 %d 被非 DSH 进程占用 (PID=%d)，请检查！", port, pid)
			}
		}
	}

	// 等待端口完全释放
	waitForPortFree(port)

	_ = os.Remove(pidFilePath())
}

func KillHarness() {
	procMu.Lock()
	defer procMu.Unlock()
	killHarnessLocked()
}

// StartWatchdog 启动后台健康巡检与主动自愈协程
func StartWatchdog() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			inspectAndHeal()
		}
	}()
}

// inspectAndHeal 主动巡检与自愈维护
func inspectAndHeal() {
	curStatus := state.Status()
	port := GetConfig().GetServerPort()

	if curStatus == StatusRunning || curStatus == StatusStarting {
		procMu.Lock()
		mp := process
		var currentPid int
		if mp != nil {
			currentPid = mp.Pid()
		}
		procMu.Unlock()

		// 检查原本被托管的 PID 是否依然存活
		if currentPid > 0 && isProcessAlive(currentPid) {
			if mp != nil {
				mp.failCount = 0
			}
			return
		}

		// 检查端口是否有活跃子进程
		if adopted := findDshPidOnPort(port); adopted > 0 {
			if mp != nil {
				mp.failCount = 0
			}
			if adopted != currentPid {
				LogInfo("接管端口 %d 活跃子进程 (PID=%d)", port, adopted)
				procMu.Lock()
				if process == mp && mp != nil {
					mp.adoptedPid = adopted
					_ = os.WriteFile(pidFilePath(), []byte(strconv.Itoa(adopted)), 0644)
				}
				procMu.Unlock()
				state.Poke()
			}
			return
		}

		// 容忍连续 3 次未就绪
		if mp != nil {
			mp.failCount++
			if mp.failCount <= 3 {
				return
			}
		}

		LogWarning("服务主进程已终止 (PID=%d) 且端口 %d 无响应，执行清理", currentPid, port)
		procMu.Lock()
		if process == mp {
			process = nil
			if currentPid > 0 {
				removePidFileIfMatches(currentPid)
			}
			stopReverseProxy()
			state.SetStatus(StatusStopped, "服务进程异常终止")
		}
		procMu.Unlock()
		if mp != nil {
			mp.closeDone()
		}
		return
	}

	if curStatus == StatusStopped {
		// 停止状态下：仅在存在残留 PID 文件且进程已死时清理一次，避免持续产生读 I/O
		pidPath := pidFilePath()
		if _, err := os.Stat(pidPath); err == nil {
			if data, err := os.ReadFile(pidPath); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
					if !isProcessAlive(pid) {
						_ = os.Remove(pidPath)
					}
				}
			}
		}
	}
}

type procSample struct {
	ticks uint64
	total uint64
}

// UsageStats CPU 与内存资源指标
type UsageStats struct {
	Cpu    string `json:"cpu"`
	Memory string `json:"memory"`
}

var (
	usageCacheMu sync.RWMutex
	cachedCpu    = "-"
	cachedMem    = "-"

	usageSubMu  sync.Mutex
	usageSubs   = make(map[chan UsageStats]struct{})
	wakeUsageCh = make(chan struct{}, 1)

	usageSampleMu sync.Mutex
	lastSample    procSample
	lastSamplePid int
	lastSampleAt  time.Time

	lastSentMu  sync.Mutex
	lastSentCpu string
	lastSentMem string
	lastSentAt  time.Time
)

// GetCachedDshUsage 获取后台单例采集的 DSH CPU 与内存指标
func GetCachedDshUsage() (string, string) {
	usageCacheMu.RLock()
	defer usageCacheMu.RUnlock()
	return cachedCpu, cachedMem
}

// SubscribeUsage 订阅 CPU/内存资源指标推送，返回的函数用于取消订阅
func SubscribeUsage(buf int) (<-chan UsageStats, func()) {
	ch := make(chan UsageStats, buf)
	usageSubMu.Lock()
	wasEmpty := len(usageSubs) == 0
	usageSubs[ch] = struct{}{}
	usageSubMu.Unlock()

	// 首个订阅者接入时立即唤醒采样，避免等待待机周期
	if wasEmpty {
		select {
		case wakeUsageCh <- struct{}{}:
		default:
		}
	}

	return ch, func() {
		usageSubMu.Lock()
		delete(usageSubs, ch)
		usageSubMu.Unlock()
	}
}

func hasUsageSubscribers() bool {
	usageSubMu.Lock()
	defer usageSubMu.Unlock()
	return len(usageSubs) > 0
}

func broadcastUsageIfChanged(stats UsageStats) {
	lastSentMu.Lock()
	now := time.Now()
	changed := stats.Cpu != lastSentCpu || stats.Memory != lastSentMem
	heartbeatDue := now.Sub(lastSentAt) >= 5*time.Second

	// 数值未变且未到保底心跳周期时跳过推送
	if !changed && !heartbeatDue {
		lastSentMu.Unlock()
		return
	}

	lastSentCpu = stats.Cpu
	lastSentMem = stats.Memory
	lastSentAt = now
	lastSentMu.Unlock()

	usageSubMu.Lock()
	defer usageSubMu.Unlock()
	for ch := range usageSubs {
		select {
		case ch <- stats:
		default:
		}
	}
}

// getTargetDshPid 获取当前需要监控的 DSH 主进程 PID（支持托管及外部过户接管）
func getTargetDshPid() int {
	port := GetConfig().GetServerPort()
	if pid := GetActiveDshPid(port); pid > 0 {
		return pid
	}
	procMu.Lock()
	defer procMu.Unlock()
	if process != nil {
		return process.Pid()
	}
	return 0
}

// StartUsageSampler 启动系统指标智能单例采集协程
func StartUsageSampler() {
	go func() {
		// 启动即采样一次建立基准 ticks
		sampleDshUsageOnce()

		for {
			// 无订阅者时彻底挂起休眠，0 CPU 唤醒
			if !hasUsageSubscribers() {
				<-wakeUsageCh
				// 首个订阅者连入唤醒时立即推送首帧，并在 200ms 后平滑校准 CPU
				sampleDshUsageOnce()
				time.Sleep(200 * time.Millisecond)
				sampleDshUsageOnce()
			}

			timer := time.NewTimer(1 * time.Second)
			select {
			case <-timer.C:
				sampleDshUsageOnce()
			case <-wakeUsageCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				sampleDshUsageOnce()
			}
		}
	}()
}

func sampleDshUsageOnce() {
	if runtime.GOOS != "linux" {
		updateAndBroadcastUsage("-", "-")
		return
	}

	targetPid := getTargetDshPid()
	if targetPid <= 0 {
		updateAndBroadcastUsage("-", "-")
		return
	}

	cpuStr, memStr := calculateDshUsage(targetPid)
	updateAndBroadcastUsage(cpuStr, memStr)
}

func updateAndBroadcastUsage(cpuStr, memStr string) {
	usageCacheMu.Lock()
	cachedCpu = cpuStr
	cachedMem = memStr
	usageCacheMu.Unlock()

	broadcastUsageIfChanged(UsageStats{Cpu: cpuStr, Memory: memStr})
}

// readProcRssKb 读取单进程常驻物理内存 (KB)
func readProcRssKb(pid int) uint64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					return kb
				}
			}
			break
		}
	}
	return 0
}

// readSystemTotalJiffies 读取系统总 CPU 时间 (所有核心累加)
func readSystemTotalJiffies() uint64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "cpu" {
			var total uint64
			for _, val := range fields[1:] {
				if num, err := strconv.ParseUint(val, 10, 64); err == nil {
					total += num
				}
			}
			return total
		}
	}
	return 0
}

// readProcTicks 读取单进程 CPU ticks (utime + stime)
func readProcTicks(pid int) (uint64, bool) {
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	content := string(statData)
	idx := strings.LastIndex(content, ")")
	if idx == -1 || idx+2 >= len(content) {
		return 0, false
	}
	fields := strings.Fields(content[idx+2:])
	if len(fields) < 13 {
		return 0, false
	}

	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	return utime + stime, true
}

// getFamilyPids 收集 rootPid 及其所有派生子孙进程 PID
func getFamilyPids(rootPid int) []int {
	if rootPid <= 0 {
		return nil
	}
	pids := []int{rootPid}

	childrenPath := fmt.Sprintf("/proc/%d/task/%d/children", rootPid, rootPid)
	data, err := os.ReadFile(childrenPath)
	if err != nil {
		return pids
	}

	for _, field := range strings.Fields(string(data)) {
		if childPid, err := strconv.Atoi(field); err == nil && childPid > 0 {
			pids = append(pids, getFamilyPids(childPid)...)
		}
	}
	return pids
}

// getDshTreeStats 聚合 主PID 及其所有派生子孙进程的 CPU 时间与常驻内存 (KB)
func getDshTreeStats(mainPid int) (totalTicks uint64, totalRssKb uint64) {
	if mainPid <= 0 || runtime.GOOS != "linux" {
		return 0, 0
	}

	familyPids := getFamilyPids(mainPid)
	for _, pid := range familyPids {
		if ticks, ok := readProcTicks(pid); ok {
			totalTicks += ticks
		}
		totalRssKb += readProcRssKb(pid)
	}

	return totalTicks, totalRssKb
}

// calculateDshUsage 计算目标进程树的 CPU 与常驻内存占用
func calculateDshUsage(pid int) (string, string) {
	if pid <= 0 || runtime.GOOS != "linux" {
		return "-", "-"
	}

	procTicks, totalRssKb := getDshTreeStats(pid)
	if procTicks == 0 && totalRssKb == 0 {
		return "-", "-"
	}

	// 格式化内存
	var memStr string
	if totalRssKb >= 1024*1024 {
		memStr = fmt.Sprintf("%.1f GB", float64(totalRssKb)/(1024*1024))
	} else {
		memStr = fmt.Sprintf("%.1f MB", float64(totalRssKb)/1024)
	}

	sysTotal := readSystemTotalJiffies()
	if sysTotal == 0 {
		return "-", memStr
	}

	usageSampleMu.Lock()
	defer usageSampleMu.Unlock()

	// 进程重启、时钟回跳或休眠过久导致跨度失真时重置样本，防止异常突刺
	if lastSamplePid != pid || procTicks < lastSample.ticks || sysTotal <= lastSample.total || time.Since(lastSampleAt) > 3*time.Second {
		lastSamplePid = pid
		lastSample = procSample{ticks: procTicks, total: sysTotal}
		lastSampleAt = time.Now()
		return "0.0%", memStr
	}

	deltaSys := sysTotal - lastSample.total
	deltaProc := procTicks - lastSample.ticks

	lastSample = procSample{ticks: procTicks, total: sysTotal}
	lastSampleAt = time.Now()

	if deltaSys == 0 || deltaProc == 0 {
		return "0.0%", memStr
	}

	// 计算相对整机总 CPU 算力的使用百分比 (0.0% ~ 100.0%)
	pct := (float64(deltaProc) / float64(deltaSys)) * 100.0
	if pct < 0 {
		pct = 0
	} else if pct > 100.0 {
		pct = 100.0
	}
	return fmt.Sprintf("%.1f%%", pct), memStr
}
