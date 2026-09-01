package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	cfg := GetConfig()
	port := cfg.ServerPort
	if port <= 0 {
		port = 2298
	}
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
	cfg := GetConfig()
	port := cfg.ServerPort
	if port <= 0 {
		port = 2298
	}

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
