package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	skillAuthMu      sync.Mutex
	currentAuthCmd   *exec.Cmd
	currentAuthStdin io.WriteCloser
	currentAuthTimer *time.Timer

	skillAuthSubsMu sync.Mutex
	skillAuthSubs   = make(map[chan gin.H]struct{})
)

// isSkillEnabled 判断当前是否已开启内置技能
func isSkillEnabled() bool {
	cfg := GetConfig()
	return cfg.EnableBuiltinSkill == nil || *cfg.EnableBuiltinSkill
}

// getTrimCliPath 获取已部署的飞牛官方命令行工具路径
func getTrimCliPath() string {
	return filepath.Join(globalDshHome, "skills", "fnos", "scripts", "trim-cli")
}

// createAuthLoginCmd 构建带伪终端支持的认证子进程命令
func createAuthLoginCmd() *exec.Cmd {
	trimPath := getTrimCliPath()
	if runtime.GOOS == "linux" {
		if scriptBin, err := exec.LookPath("script"); err == nil {
			cmdStr := fmt.Sprintf("%s login --no-open", trimPath)
			return exec.Command(scriptBin, "-qec", cmdStr, "/dev/null")
		}
	}
	return exec.Command(trimPath, "login", "--no-open")
}

// SubscribeSkillAuth 订阅技能授权状态变更事件
func SubscribeSkillAuth(buf int) (<-chan gin.H, func()) {
	skillAuthSubsMu.Lock()
	ch := make(chan gin.H, buf)
	skillAuthSubs[ch] = struct{}{}
	skillAuthSubsMu.Unlock()

	return ch, func() {
		skillAuthSubsMu.Lock()
		delete(skillAuthSubs, ch)
		skillAuthSubsMu.Unlock()
	}
}

// broadcastSkillAuth 向所有 WebSocket 客户端广播最新授权状态
func broadcastSkillAuth() {
	payload := getSkillAuthPayload()
	skillAuthSubsMu.Lock()
	defer skillAuthSubsMu.Unlock()
	for ch := range skillAuthSubs {
		select {
		case ch <- payload:
		default:
		}
	}
}

// getSkillAuthPayload 获取当前飞牛技能授权状态快照
func getSkillAuthPayload() gin.H {
	if !isSkillEnabled() {
		return gin.H{"authorized": false}
	}

	activeEnc := filepath.Join(globalHomeDir, ".config", "trim-cli", "secure", "active.enc")
	if _, err := os.Stat(activeEnc); err != nil {
		return gin.H{"authorized": false}
	}

	cmd := exec.Command(getTrimCliPath(), "user", "info")
	cmd.Env = append(os.Environ(), "HOME="+globalHomeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return gin.H{"authorized": false}
	}

	var u struct {
		Username string `json:"username"`
	}
	_ = json.Unmarshal(out, &u)
	return gin.H{"authorized": true, "username": u.Username}
}

// cancelCurrentSkillAuth 取消并清理当前未完成的授权交互会话
func cancelCurrentSkillAuth() {
	if currentAuthTimer != nil {
		currentAuthTimer.Stop()
		currentAuthTimer = nil
	}
	if currentAuthStdin != nil {
		_ = currentAuthStdin.Close()
		currentAuthStdin = nil
	}
	if currentAuthCmd != nil && currentAuthCmd.Process != nil {
		_ = currentAuthCmd.Process.Kill()
		currentAuthCmd = nil
	}
}

// handleSkillAuthStart 发起 OAuth 2.0 登录交互并获取授权 URL
func handleSkillAuthStart(c *gin.Context) {
	if !isSkillEnabled() {
		Fail(c, http.StatusBadRequest, "未启用飞牛官方技能")
		return
	}

	skillAuthMu.Lock()
	defer skillAuthMu.Unlock()

	cancelCurrentSkillAuth()
	cleanLegacyTrimSession()

	cmd := createAuthLoginCmd()
	cmd.Dir = filepath.Join(globalDshHome, "skills", "fnos")
	cmd.Env = append(os.Environ(),
		"HOME="+globalHomeDir,
		"TRIM_CLI_CONFIG_DIR="+filepath.Join(globalHomeDir, ".config", "trim-cli"),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		Fail(c, http.StatusInternalServerError, "创建输入管道失败: "+err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		Fail(c, http.StatusInternalServerError, "创建输出管道失败: "+err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		Fail(c, http.StatusInternalServerError, "创建错误管道失败: "+err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		Fail(c, http.StatusInternalServerError, "启动认证进程失败: "+err.Error())
		return
	}

	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	var (
		outMu       sync.Mutex
		outputLines []string
		wg          sync.WaitGroup
	)

	scanStream := func(r io.Reader, tag string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			LogInfo("[技能认证进程 %s] %s", tag, line)
			outMu.Lock()
			outputLines = append(outputLines, line)
			outMu.Unlock()

			if strings.Contains(line, "/signin?") {
				fields := strings.Fields(line)
				found := ""
				for _, f := range fields {
					if strings.Contains(f, "/signin?") {
						found = f
						break
					}
				}
				if found == "" {
					found = strings.TrimSpace(line)
				}
				if idx := strings.Index(found, "/signin?"); idx != -1 {
					found = found[idx:]
				}
				select {
				case urlChan <- found:
				default:
				}
			}
		}
		if err := scanner.Err(); err != nil {
			LogError("[技能认证进程 %s] 读取异常: %v", tag, err)
		}
	}

	wg.Add(2)
	go scanStream(stdout, "stdout")
	go scanStream(stderr, "stderr")

	go func() {
		wg.Wait()
		outMu.Lock()
		collected := strings.Join(outputLines, " | ")
		outMu.Unlock()
		select {
		case errChan <- fmt.Errorf("未在输出中找到授权链接 (输出内容: %s)", collected):
		default:
		}
	}()

	select {
	case authURL := <-urlChan:
		currentAuthCmd = cmd
		currentAuthStdin = stdin
		currentAuthTimer = time.AfterFunc(5*time.Minute, func() {
			skillAuthMu.Lock()
			defer skillAuthMu.Unlock()
			cancelCurrentSkillAuth()
		})
		OK(c, gin.H{"url": authURL})
	case err := <-errChan:
		cancelCurrentSkillAuth()
		Fail(c, http.StatusInternalServerError, "获取授权链接失败: "+err.Error())
	case <-time.After(8 * time.Second):
		cancelCurrentSkillAuth()
		Fail(c, http.StatusGatewayTimeout, "等待授权链接超时")
	}
}

// handleSkillAuthConfirm 接收并验证授权码完成登录
func handleSkillAuthConfirm(c *gin.Context) {
	if !isSkillEnabled() {
		Fail(c, http.StatusBadRequest, "未启用飞牛官方技能")
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		Fail(c, http.StatusBadRequest, "授权码不能为空")
		return
	}

	skillAuthMu.Lock()
	cmd := currentAuthCmd
	stdin := currentAuthStdin
	if cmd == nil || stdin == nil {
		skillAuthMu.Unlock()
		Fail(c, http.StatusBadRequest, "无正在等待授权码的登录会话，请重新发起")
		return
	}

	_, writeErr := stdin.Write([]byte(strings.TrimSpace(req.Code) + "\n"))
	_ = stdin.Close()
	currentAuthStdin = nil
	if currentAuthTimer != nil {
		currentAuthTimer.Stop()
		currentAuthTimer = nil
	}
	skillAuthMu.Unlock()

	if writeErr != nil {
		skillAuthMu.Lock()
		cancelCurrentSkillAuth()
		skillAuthMu.Unlock()
		Fail(c, http.StatusInternalServerError, "发送授权码失败: "+writeErr.Error())
		return
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		skillAuthMu.Lock()
		currentAuthCmd = nil
		skillAuthMu.Unlock()
		if err != nil {
			Fail(c, http.StatusInternalServerError, "授权码验证失败: "+err.Error())
			return
		}
		LogInfo("[技能] 飞牛官方内置技能授权成功")
		broadcastSkillAuth()
		OKMsg(c, "授权成功", gin.H{"authorized": true})
	case <-time.After(15 * time.Second):
		skillAuthMu.Lock()
		cancelCurrentSkillAuth()
		skillAuthMu.Unlock()
		Fail(c, http.StatusGatewayTimeout, "验证超时")
	}
}

// handleSkillAuthLogout 退出登录并清除本地飞牛技能会话
func handleSkillAuthLogout(c *gin.Context) {
	if !isSkillEnabled() {
		Fail(c, http.StatusBadRequest, "未启用飞牛官方技能")
		return
	}

	cmd := exec.Command(getTrimCliPath(), "logout")
	cmd.Env = append(os.Environ(), "HOME="+globalHomeDir)
	_ = cmd.Run()

	cfgDir := filepath.Join(globalHomeDir, ".config", "trim-cli")
	_ = os.RemoveAll(filepath.Join(cfgDir, "secure"))
	_ = os.Remove(filepath.Join(cfgDir, "session.json"))

	LogInfo("[技能] 飞牛官方内置技能已解除授权")
	broadcastSkillAuth()
	OKMsg(c, "已退出登录并清除会话", gin.H{"authorized": false})
}
