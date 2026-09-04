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
	return filepath.Join(pkgVarDir, "harness.pid")
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
	out, err := exec.Command("ps", "-o", "pgid=", "-p", strconv.Itoa(pid)).Output()
	if err == nil {
		pgidStr := strings.TrimSpace(string(out))
		if pgid, err := strconv.Atoi(pgidStr); err == nil && pgid > 0 {
			if killProcessGroup(pgid) {
				return
			}
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

// findDshPidsOnPort 获取指定端口上的 DSH 进程 PID 列表
func findDshPidsOnPort(port int) []int {
	var dshPids []int
	for _, pid := range findPidsOnPort(port) {
		if isProcessAlive(pid) && isDshProcess(pid) {
			dshPids = append(dshPids, pid)
		}
	}
	return dshPids
}

// findDshPidOnPort 获取指定端口上的首个 DSH 进程 PID
func findDshPidOnPort(port int) int {
	pids := findDshPidsOnPort(port)
	if len(pids) > 0 {
		return pids[0]
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
		// 停止状态下：仅清理失效的 PID 残留文件
		if data, err := os.ReadFile(pidFilePath()); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				if !isProcessAlive(pid) {
					_ = os.Remove(pidFilePath())
				}
			}
		}
	}
}

type procSample struct {
	ticks uint64
	total uint64
}

var (
	usageCacheMu sync.RWMutex
	cachedCpu    = "-"
	cachedMem    = "-"

	usageSampleMu sync.Mutex
	lastSample    procSample
	lastSamplePid int
	lastSampleAt  time.Time
)

// GetCachedDshUsage 获取后台单例采集的 DSH CPU 与内存指标
func GetCachedDshUsage() (string, string) {
	usageCacheMu.RLock()
	defer usageCacheMu.RUnlock()
	return cachedCpu, cachedMem
}

// StartUsageSampler 启动系统指标单例采集协程
func StartUsageSampler() {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sampleDshUsageOnce()
		}
	}()
}

func sampleDshUsageOnce() {
	if runtime.GOOS != "linux" {
		usageCacheMu.Lock()
		cachedCpu = "-"
		cachedMem = "-"
		usageCacheMu.Unlock()
		return
	}

	cpuStr, memStr := calculateDshUsage(os.Getpid())
	usageCacheMu.Lock()
	cachedCpu = cpuStr
	cachedMem = memStr
	usageCacheMu.Unlock()
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

// parseProcStat 解析 /proc/[pid]/stat，提取 ppid, pgrp 以及 CPU ticks (utime + stime)
func parseProcStat(pid int) (ppid, pgrp int, ticks uint64, ok bool) {
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, 0, false
	}
	content := string(statData)
	idx := strings.LastIndex(content, ")")
	if idx == -1 || idx+2 >= len(content) {
		return 0, 0, 0, false
	}
	fields := strings.Fields(content[idx+2:])
	if len(fields) < 13 {
		return 0, 0, 0, false
	}

	ppid, _ = strconv.Atoi(fields[1])
	pgrp, _ = strconv.Atoi(fields[2])
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	return ppid, pgrp, utime + stime, true
}

// getDshTreeStats 聚合 主PID 及其所有派生子孙进程的 CPU 时间与常驻内存 (KB)
func getDshTreeStats(mainPid int) (totalTicks uint64, totalRssKb uint64) {
	if mainPid <= 0 || runtime.GOOS != "linux" {
		return 0, 0
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		_, _, ticks, _ := parseProcStat(mainPid)
		return ticks, readProcRssKb(mainPid)
	}

	type procItem struct {
		ppid  int
		ticks uint64
		rss   uint64
	}

	procMap := make(map[int]procItem, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		ppid, _, ticks, ok := parseProcStat(pid)
		if !ok {
			continue
		}
		procMap[pid] = procItem{
			ppid:  ppid,
			ticks: ticks,
			rss:   readProcRssKb(pid),
		}
	}

	isFamily := make(map[int]bool, len(procMap))
	isFamily[mainPid] = true

	var checkFamily func(pid int, visited map[int]bool) bool
	checkFamily = func(pid int, visited map[int]bool) bool {
		if pid <= 0 {
			return false
		}
		if res, exists := isFamily[pid]; exists {
			return res
		}
		if visited[pid] {
			return false
		}
		visited[pid] = true

		item, exists := procMap[pid]
		if !exists {
			return false
		}
		if checkFamily(item.ppid, visited) {
			isFamily[pid] = true
			return true
		}
		isFamily[pid] = false
		return false
	}

	for pid, item := range procMap {
		if checkFamily(pid, make(map[int]bool)) {
			totalTicks += item.ticks
			totalRssKb += item.rss
		}
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

	// 进程重启、时钟回跳或子进程退出导致 ticks 减少时重置样本，防止无符号下溢
	if lastSamplePid != pid || procTicks < lastSample.ticks || sysTotal <= lastSample.total {
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
