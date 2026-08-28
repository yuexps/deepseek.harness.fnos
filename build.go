package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoURL    = "https://github.com/deepseek-ai/deepseek-harness"
	nodeBinDir = "/var/apps/nodejs_v24/target/bin"
	gitBin     = "/usr/bin/git"
)

func nodeBin() string { return filepath.Join(nodeBinDir, "node") }
func npmBin() string  { return filepath.Join(nodeBinDir, "npm") }
func pnpmBin() string { return filepath.Join(pkgVarDir, "pnpm-env", "node_modules", ".bin", "pnpm") }

// CheckUpdateResult 检查更新返回结构
type CheckUpdateResult struct {
	HasUpdate         bool   `json:"has_update"`
	CurrentVersion    string `json:"current_version"`
	RemoteVersion     string `json:"remote_version"`
	CurrentCommit     string `json:"current_commit"`
	RemoteCommit      string `json:"remote_commit"`
	RemoteShortCommit string `json:"remote_short_commit"`
	Message           string `json:"message"`
}

func readRemoteVersion() string {
	cmd := gitCmd("-C", srcDir, "show", "FETCH_HEAD:package.json")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(out, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

func formatVersionTag(ver, commit string) string {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	shortCommit := commit
	if len(shortCommit) > 7 {
		shortCommit = shortCommit[:7]
	}
	if ver != "" && shortCommit != "" && shortCommit != "-" {
		return fmt.Sprintf("v%s (%s)", ver, shortCommit)
	}
	if ver != "" {
		return "v" + ver
	}
	if shortCommit != "" && shortCommit != "-" {
		return shortCommit
	}
	return "-"
}

// isSourceValid 检查源码工作区是否完整有效（目录存在且包含关键 package.json 文件）
func isSourceValid() bool {
	if fi, err := os.Stat(srcDir); err != nil || !fi.IsDir() {
		return false
	}
	if fi, err := os.Stat(filepath.Join(srcDir, "package.json")); err != nil || fi.IsDir() {
		return false
	}
	return true
}

// CheckUpdate 轻量快速检查远程仓库是否有更新，带 15s 超时，绝不中断正在运行的服务
func CheckUpdate() (*CheckUpdateResult, error) {
	if !isSourceValid() {
		return &CheckUpdateResult{
			HasUpdate: true,
			Message:   "本地源码未就绪，需初始化拉取",
		}, nil
	}

	currentCommit := gitHead()
	currentVersion := readVersion()
	tagCurrent := formatVersionTag(currentVersion, currentCommit)
	LogInfo("开始检查远程仓库更新 (当前版本: %s)...", tagCurrent)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_ = configureSparseCheckout()
	fetchCmd := gitCmdContext(ctx, "-C", srcDir, "fetch", "--depth=1", "origin")
	setProcessGroup(fetchCmd)
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			LogWarning("检查更新超时 (15s)，请检查网络连接或代理设置")
			return nil, fmt.Errorf("检查更新超时 (15s)，请检查网络连接或代理设置")
		}
		errMsg := fmt.Sprintf("检查更新失败: %s (%s)", err, strings.TrimSpace(string(out)))
		LogWarning("%s", errMsg)
		return nil, fmt.Errorf("%s", errMsg)
	}

	fetchHeadCmd := gitCmd("-C", srcDir, "rev-parse", "FETCH_HEAD")
	fetchHeadOut, err := fetchHeadCmd.Output()
	if err != nil {
		LogWarning("解析远程版本信息失败: %s", err)
		return nil, fmt.Errorf("解析远程版本信息失败: %w", err)
	}
	remoteCommit := strings.TrimSpace(string(fetchHeadOut))
	remoteVersion := readRemoteVersion()
	if remoteVersion == "" {
		remoteVersion = currentVersion
	}

	shortRemote := remoteCommit
	if len(shortRemote) > 7 {
		shortRemote = shortRemote[:7]
	}

	tagRemote := formatVersionTag(remoteVersion, remoteCommit)

	hasUpdate := currentCommit != "" && remoteCommit != "" && currentCommit != remoteCommit
	var msg string
	if hasUpdate {
		msg = fmt.Sprintf("发现新版本 [ %s → %s ]", tagCurrent, tagRemote)
		LogInfo("检查远程更新完成: %s", msg)
	} else {
		msg = fmt.Sprintf("当前已是最新版本 [ %s ]", tagCurrent)
		LogInfo("检查远程更新完成: %s", msg)
	}

	return &CheckUpdateResult{
		HasUpdate:         hasUpdate,
		CurrentVersion:    currentVersion,
		RemoteVersion:     remoteVersion,
		CurrentCommit:     currentCommit,
		RemoteCommit:      remoteCommit,
		RemoteShortCommit: shortRemote,
		Message:           msg,
	}, nil
}

func Upgrade() {
	state.SetStatus(StatusBuilding, "正在准备更新...")
	go update(false)
}

func Rebuild() {
	state.SetStatus(StatusBuilding, "正在准备强制重建...")
	go update(true)
}

// safeRemoveAll 安全递归删除文件与目录，遇到只读权限文件时自动解除只读以彻底清理
func safeRemoveAll(path string) error {
	if err := os.RemoveAll(path); err == nil {
		return nil
	}
	// 针对只读或权限受阻的文件递归解除只读权限后再次删除
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0777)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// RepairEnvironment 恢复出厂设置：清空第三方插件与挂载配置，重新部署纯净运行环境
func RepairEnvironment() {
	state.SetStatus(StatusBuilding, "正在准备恢复出厂设置...")
	go repairEnvironment()
}

func repairEnvironment() {
	tarPath := filepath.Join(appDest, "deepseek-harness.tar.gz")
	if _, err := os.Stat(tarPath); err != nil {
		LogWarning("未检测到内置离线包，无法执行恢复出厂设置: %s", tarPath)
		state.SetStatus(StatusStopped, "未检测到内置离线包，无法恢复出厂设置")
		return
	}

	stopAndWait()
	ResetAllProfilePatches()

	state.SetStatus(StatusBuilding, "正在清空工作区并恢复出厂状态...")
	LogInfo("开始执行恢复出厂设置（清理第三方插件与挂载，保留 API 凭据与配置）")

	zipVer := readAppDestVersion()
	deployPrebuilt(tarPath, zipVer, false)
}

// formatGitError 根据实际错误特征返回精准的状态提示（在确认网络受阻时引导配置代理）
func formatGitError(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	errStr := err.Error()
	errLower := strings.ToLower(errStr)
	if strings.Contains(errLower, "could not resolve host") ||
		strings.Contains(errLower, "failed to connect") ||
		strings.Contains(errLower, "connection timed out") ||
		strings.Contains(errLower, "connection refused") ||
		strings.Contains(errLower, "ssl") ||
		strings.Contains(errLower, "gnutls") ||
		strings.Contains(errLower, "network is unreachable") ||
		strings.Contains(errLower, "timed out") {
		return fmt.Sprintf("%s（网络连接受阻，请在【应用设置】配置代理）: %s", prefix, errStr)
	}
	return fmt.Sprintf("%s: %s", prefix, errStr)
}

func update(forceRebuild bool) {
	stopAndWait()

	if !isSourceValid() {
		state.SetStatus(StatusBuilding, "源码未就绪或文件损坏，正在重新初始化源码...")
		LogInfo("源码工作区无效或损坏 (%s)，正在清理并重新克隆: %s", srcDir, repoURL)
		_ = os.RemoveAll(srcDir)
		if err := gitClone(); err != nil {
			LogWarning("Git 克隆失败: %s", err)
			state.SetStatus(StatusStopped, formatGitError("克隆源码失败", err))
			return
		}
		if err := buildFromSource(false); err != nil {
			LogWarning("源码构建失败: %s", err)
			state.SetStatus(StatusStopped, "构建失败: "+err.Error())
			return
		}
		refreshCommit()
		restartService()
		return
	}

	if forceRebuild {
		state.SetStatus(StatusBuilding, "正在准备强制重建（恢复完整源码环境并重新安装依赖全量编译）...")
		_ = gitCmd("-C", srcDir, "reset", "--hard", "HEAD").Run()
		_ = gitCmd("-C", srcDir, "clean", "-fdx", "-e", "node_modules").Run()
	} else {
		commitBefore := gitHead()
		versionBefore := readVersion()
		tagBefore := formatVersionTag(versionBefore, commitBefore)
		state.SetStatus(StatusBuilding, "正在拉取远程更新...")
		if err := gitPull(); err != nil {
			LogWarning("Git 拉取失败: %s", err)
			state.SetStatus(StatusStopped, formatGitError("拉取更新失败", err))
			return
		}
		commitAfter := gitHead()
		versionAfter := readVersion()
		tagAfter := formatVersionTag(versionAfter, commitAfter)
		if commitBefore != "" && commitBefore == commitAfter {
			LogInfo("当前已是最新版本 [ %s ]，跳过构建", tagAfter)
			state.SetStatus(StatusBuilding, fmt.Sprintf("当前已是最新版本 [ %s ]，跳过构建", tagAfter))
			refreshCommit()
			restartService()
			return
		}
		shortTarget := commitAfter
		if len(shortTarget) > 7 {
			shortTarget = shortTarget[:7]
		}
		state.SetTargetCommit(shortTarget)
		LogInfo("检测到版本更新 [ %s → %s ]，开始构建", tagBefore, tagAfter)
		state.SetStatus(StatusBuilding, fmt.Sprintf("检测到版本更新 [ %s → %s ]，正在同步依赖与构建...", tagBefore, tagAfter))
	}

	if err := buildFromSource(forceRebuild); err != nil {
		LogWarning("源码构建失败: %s", err)
		state.SetStatus(StatusStopped, "构建失败: "+err.Error())
		return
	}

	refreshCommit()
	restartService()
}

func configureSparseCheckout() error {
	cmd := gitCmd("-C", srcDir, "sparse-checkout", "set", "packages", "apps", "vendor", "native", "patches", "scripts", "website")
	cmd.Stdout = NewLogWriterInfo()
	cmd.Stderr = NewLogWriterWarn()
	return cmd.Run()
}

func gitClone() error {
	_ = os.MkdirAll(filepath.Dir(srcDir), 0755)
	cmd := gitCmd("clone", "--depth=1", repoURL, srcDir)
	cmd.Stdout = NewLogWriterInfo()
	cmd.Stderr = NewLogWriterWarn()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	_ = configureSparseCheckout()
	return nil
}

func gitPull() error {
	_ = configureSparseCheckout()
	fetchCmd := gitCmd("-C", srcDir, "fetch", "--depth=1", "origin")
	fetchCmd.Stdout = NewLogWriterInfo()
	fetchCmd.Stderr = NewLogWriterWarn()
	if err := fetchCmd.Run(); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}

	resetCmd := gitCmd("-C", srcDir, "reset", "--hard", "FETCH_HEAD")
	resetCmd.Stdout = NewLogWriterInfo()
	resetCmd.Stderr = NewLogWriterWarn()
	if err := resetCmd.Run(); err != nil {
		return fmt.Errorf("git reset: %w", err)
	}

	// 清理未跟踪的历史生成物，保留依赖缓存
	cleanCmd := gitCmd("-C", srcDir, "clean", "-fdx", "-e", "node_modules")
	cleanCmd.Stdout = NewLogWriterInfo()
	cleanCmd.Stderr = NewLogWriterWarn()
	_ = cleanCmd.Run()
	return nil
}

func gitHead() string {
	cmd := gitCmd("-C", srcDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func extractTarGz(tarPath, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	cmd := exec.Command("tar", "--no-same-owner", "-xzf", tarPath, "-C", dst)
	cmd.Stdout = NewLogWriterInfo()
	cmd.Stderr = NewLogWriterWarn()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("解压 tar.gz 失败: %w", err)
	}
	return nil
}

func installPnpm() error {
	if _, err := os.Stat(pnpmBin()); err == nil {
		return nil
	}
	pnpmDir := filepath.Join(pkgVarDir, "pnpm-env")
	_ = os.MkdirAll(pnpmDir, 0755)
	return runCmd(pnpmDir, npmBin(), "install", "pnpm", "--registry=https://registry.npmmirror.com")
}

func ensureTool(bin, pkg string) error {
	if _, err := exec.LookPath(bin); err == nil {
		return nil
	}
	return fmt.Errorf("系统未检测到 %s 编译工具链，请在宿主机终端执行: sudo apt-get update && sudo apt-get install -y %s", bin, pkg)
}

func landlockNativeDir() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return filepath.Join(srcDir, "native", "landlock-run", "packages", "linux-"+arch)
}

func landlockBinPath() string {
	return filepath.Join(landlockNativeDir(), "bin", "landlock-run")
}

func buildLandlock() error {
	state.SetStatus(StatusBuilding, "正在构建 landlock 沙箱组件...")
	LogInfo("开始编译 landlock 原生沙箱组件")
	if err := ensureTool("musl-gcc", "musl-tools"); err != nil {
		return err
	}
	landlockDir := filepath.Join(srcDir, "native", "landlock-run")
	if _, err := os.Stat(landlockDir); err == nil {
		if err := runCmd(landlockDir, pnpmBin(), "run", "build:native"); err == nil {
			bin := landlockBinPath()
			_ = os.Chmod(bin, 0755)
			LogInfo("landlock 原生沙箱组件构建完成: %s", bin)
			return nil
		}
	}
	if err := runCmd(srcDir, pnpmBin(), "--filter", "@deepseek-ai/node-addon-landlock-run-workspace", "run", "build:native"); err != nil {
		return fmt.Errorf("landlock build:native: %w", err)
	}
	bin := landlockBinPath()
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("landlock 构建完成但产物缺失: %s", bin)
	}
	_ = os.Chmod(bin, 0755)
	LogInfo("landlock 原生沙箱组件构建完成: %s", bin)
	return nil
}

// buildFromSource 专用于在线升级拉取或强制重建时的源码编译流程
func buildFromSource(forceClean bool) error {
	state.SetStatus(StatusBuilding, "正在准备编译环境...")
	if err := ensureTool("gcc", "build-essential"); err != nil {
		return err
	}
	if err := installPnpm(); err != nil {
		return fmt.Errorf("install pnpm: %w", err)
	}

	state.SetStatus(StatusBuilding, "正在安装依赖...")
	pnpmArgs := []string{"install", "--prefer-offline", "--config.confirm-modules-purge=false", "--registry", "https://registry.npmmirror.com"}
	if forceClean {
		pnpmArgs = append(pnpmArgs, "--force")
	}
	if err := runCmd(srcDir, pnpmBin(), pnpmArgs...); err != nil {
		return fmt.Errorf("pnpm install: %w", err)
	}

	// 清理历史项目引用产物与构建缓存
	_ = runCmd(srcDir, pnpmBin(), "run", "clean")

	state.SetStatus(StatusBuilding, "正在编译项目源码 (official)...")
	if err := runCmd(srcDir, pnpmBin(), "run", "build:official"); err != nil {
		return fmt.Errorf("pnpm run build:official: %w", err)
	}
	LogInfo("项目源码编译完成")

	if err := buildLandlock(); err != nil {
		return err
	}

	SetBuildTime(time.Now())
	return nil
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = NewLogWriterInfo()
	cmd.Stderr = NewLogWriterWarn()
	return cmd.Run()
}

func gitBaseArgs() []string {
	args := []string{"-c", "safe.directory=*"}
	cfg := GetConfig()
	if cfg.NetworkProxy != "" {
		args = append(args,
			"-c", "http.proxy="+cfg.NetworkProxy,
			"-c", "https.proxy="+cfg.NetworkProxy,
		)
	}
	return args
}

func gitCmd(extraArgs ...string) *exec.Cmd {
	args := append(gitBaseArgs(), extraArgs...)
	return exec.Command(gitBin, args...)
}

func gitCmdContext(ctx context.Context, extraArgs ...string) *exec.Cmd {
	args := append(gitBaseArgs(), extraArgs...)
	return exec.CommandContext(ctx, gitBin, args...)
}

func refreshCommit() {
	cmd := gitCmd("-C", srcDir, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	commit := ""
	if err == nil {
		commit = strings.TrimSpace(string(out))
	}
	if commit == "" {
		if data, err := os.ReadFile(filepath.Join(srcDir, ".commit")); err == nil {
			commit = strings.TrimSpace(string(data))
		} else if data, err := os.ReadFile(filepath.Join(appDest, ".commit")); err == nil {
			commit = strings.TrimSpace(string(data))
		}
	}
	if commit == "" {
		commit = "-"
	}
	SetCommit(commit)
	SetVersion(readVersion())
}

func readVersion() string {
	data, err := os.ReadFile(filepath.Join(srcDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

func readAppDestVersion() string {
	data, err := os.ReadFile(filepath.Join(appDest, ".version"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
