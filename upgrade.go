package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	dshPackageName = "@deepseek-ai/dsh"
	nodeBinDir     = "/var/apps/nodejs_v24/target/bin"
	minDshVersion  = "0.2.1-rc.1"
)

func findBin(preferred, fallback string) string {
	if _, err := os.Stat(preferred); err == nil {
		return preferred
	}
	if p, err := exec.LookPath(fallback); err == nil {
		return p
	}
	return preferred
}

func nodeBin() string { return findBin(filepath.Join(nodeBinDir, "node"), "node") }
func npmBin() string  { return findBin(filepath.Join(nodeBinDir, "npm"), "npm") }
func pnpmBin() string { return filepath.Join(globalPnpmDir, "node_modules", ".bin", "pnpm") }

var pnpmInstallMu sync.Mutex

// installPnpm 确保插件管理所需的 pnpm 运行环境就绪
func installPnpm() error {
	if _, err := os.Stat(pnpmBin()); err == nil {
		return nil
	}
	pnpmInstallMu.Lock()
	defer pnpmInstallMu.Unlock()

	// 二次校验防并发重复初始化
	if _, err := os.Stat(pnpmBin()); err == nil {
		return nil
	}
	pnpmDir := globalPnpmDir
	_ = os.MkdirAll(pnpmDir, 0755)

	cfg := GetConfig()
	args := []string{"install", "pnpm", "--no-audit", "--no-fund", "--registry=" + cfg.GetNpmRegistry()}
	cmd := exec.Command(npmBin(), args...)
	cmd.Dir = pnpmDir
	cmd.Stdout = NewLogWriter()
	cmd.Stderr = NewLogWriter()
	if cfg.NetworkProxy != "" && strings.Contains(cfg.GetNpmRegistry(), "npmjs.org") {
		cmd.Env = append(os.Environ(),
			"HTTP_PROXY="+cfg.NetworkProxy,
			"HTTPS_PROXY="+cfg.NetworkProxy,
			"http_proxy="+cfg.NetworkProxy,
			"https_proxy="+cfg.NetworkProxy,
		)
	}
	LogInfo("[升级] 正在初始化 pnpm 环境...")
	if err := cmd.Run(); err != nil {
		LogWarning("[升级] 安装 pnpm 失败: %s", err)
		return fmt.Errorf("安装 pnpm 失败: %w", err)
	}
	LogInfo("[升级] pnpm 环境初始化成功: %s", pnpmBin())
	return nil
}

// DshVersionsResult 在线部署可选版本
type DshVersionsResult struct {
	CurrentVersion     string   `json:"current_version"`
	Versions           []string `json:"versions"`
	RecommendedVersion string   `json:"recommended_version"`
	MinVersion         string   `json:"min_version"`
}

// CheckUpdateResult 检查更新返回结构
type CheckUpdateResult struct {
	HasUpdate      bool   `json:"has_update"`
	CurrentVersion string `json:"current_version"`
	RemoteVersion  string `json:"remote_version"`
	Message        string `json:"message"`
}

type npmPackageInfo struct {
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
}

// fetchRemoteNpmInfo 查询包的版本元数据
func fetchRemoteNpmInfo(pkgName string) (*npmPackageInfo, error) {
	cfg := GetConfig()
	reg := strings.TrimRight(cfg.GetNpmRegistry(), "/")
	apiURL := fmt.Sprintf("%s/%s", reg, pkgName)

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if cfg.NetworkProxy != "" && strings.Contains(reg, "npmjs.org") {
		if pURL, err := url.Parse(cfg.NetworkProxy); err == nil {
			transport.Proxy = http.ProxyURL(pURL)
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "DeepSeek-Harness-FNOS")

	resp, err := client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var info npmPackageInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err == nil && (len(info.DistTags) > 0 || len(info.Time) > 0) {
			return &info, nil
		}
	}
	if resp != nil {
		_ = resp.Body.Close()
	}

	cmdArgs := []string{"view", pkgName, "dist-tags", "time", "--json", "--registry=" + reg}
	cmd := exec.Command(npmBin(), cmdArgs...)
	if cfg.NetworkProxy != "" && strings.Contains(reg, "npmjs.org") {
		cmd.Env = append(os.Environ(),
			"HTTP_PROXY="+cfg.NetworkProxy,
			"HTTPS_PROXY="+cfg.NetworkProxy,
			"http_proxy="+cfg.NetworkProxy,
			"https_proxy="+cfg.NetworkProxy,
		)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("获取 NPM 版本元数据失败: %w", err)
	}

	var info npmPackageInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("解析 NPM 版本元数据失败: %w", err)
	}
	return &info, nil
}

// collectAdaptedVersions 过滤并按语义化版本降序返回适配版本。
func collectAdaptedVersions(info *npmPackageInfo) []string {
	if info == nil {
		return nil
	}

	versions := make([]string, 0, len(info.Time))
	for ver := range info.Time {
		cleanVer := strings.TrimPrefix(strings.TrimSpace(ver), "v")
		if _, ok := parseSemver(cleanVer); !ok || CompareSemver(cleanVer, minDshVersion) < 0 {
			continue
		}
		versions = append(versions, cleanVer)
	}
	sort.Slice(versions, func(i, j int) bool {
		return CompareSemver(versions[i], versions[j]) > 0
	})
	return versions
}

// ListDshVersions 返回在线部署可选择的适配版本。
func ListDshVersions() (*DshVersionsResult, error) {
	info, err := fetchRemoteNpmInfo(dshPackageName)
	if err != nil {
		return nil, fmt.Errorf("获取 DSH 版本列表失败: %w", err)
	}
	versions := collectAdaptedVersions(info)
	currentVersion := strings.TrimPrefix(strings.TrimSpace(readVersion()), "v")
	if _, ok := parseSemver(currentVersion); ok {
		upgradable := make([]string, 0, len(versions))
		for _, version := range versions {
			if CompareSemver(version, currentVersion) >= 0 {
				upgradable = append(upgradable, version)
			}
		}
		versions = upgradable
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("未找到可部署版本 (最低 v%s)", minDshVersion)
	}

	return &DshVersionsResult{
		CurrentVersion:     currentVersion,
		Versions:           versions,
		RecommendedVersion: versions[0],
		MinVersion:         minDshVersion,
	}, nil
}

// resolveTargetVersion 依据更新通道解析目标版本。
func resolveTargetVersion(info *npmPackageInfo, channel string) string {
	if info == nil {
		return ""
	}

	if channel == "newest" {
		var maxVer string
		for ver := range info.Time {
			if ver == "modified" || ver == "created" {
				continue
			}
			cleanVer := strings.TrimPrefix(strings.TrimSpace(ver), "v")
			if _, ok := parseSemver(cleanVer); !ok {
				continue
			}
			if maxVer == "" || CompareSemver(cleanVer, maxVer) > 0 {
				maxVer = cleanVer
			}
		}
		return maxVer
	}

	ver, ok := info.DistTags[channel]
	if !ok {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(ver), "v")
}

func formatVersionTag(ver string) string {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	if ver != "" {
		return "v" + ver
	}
	return "-"
}

// CheckUpdate 轻量快速检查远程 NPM 是否有更新，带 10s 超时，绝不中断正在运行的服务
func CheckUpdate() (*CheckUpdateResult, error) {
	currentVersion := readVersion()
	tagCurrent := formatVersionTag(currentVersion)
	reg := strings.TrimRight(GetConfig().GetNpmRegistry(), "/")
	channel := GetConfig().GetUpdateChannel()
	LogInfo("[升级] 开始检查远程版本更新 (包名=%s, 当前版本=%s, 渠道=%s, 源=%s)", dshPackageName, tagCurrent, channel, reg)

	info, err := fetchRemoteNpmInfo(dshPackageName)
	if err != nil {
		LogWarning("[升级] 检查远程版本更新失败 (包名=%s): %s", dshPackageName, err)
		return nil, fmt.Errorf("检查更新失败: %w", err)
	}

	remoteVersion := resolveTargetVersion(info, channel)
	if remoteVersion == "" {
		remoteVersion = currentVersion
	}
	tagRemote := formatVersionTag(remoteVersion)

	hasUpdate := false
	if currentVersion == "" {
		hasUpdate = true
	} else if remoteVersion != "" && CompareSemver(remoteVersion, currentVersion) > 0 {
		hasUpdate = true
	}

	var msg string
	if hasUpdate {
		if currentVersion == "" {
			msg = fmt.Sprintf("发现可用版本 [ %s ]，需初始化安装", tagRemote)
		} else {
			msg = fmt.Sprintf("发现新版本 [ %s → %s ]", tagCurrent, tagRemote)
		}
		LogInfo("[升级] 检查更新完成: %s", msg)
	} else {
		msg = fmt.Sprintf("当前已是最新版本 [ %s ]", tagCurrent)
		LogInfo("[升级] 检查更新完成: %s", msg)
	}

	return &CheckUpdateResult{
		HasUpdate:      hasUpdate,
		CurrentVersion: currentVersion,
		RemoteVersion:  remoteVersion,
		Message:        msg,
	}, nil
}

// Upgrade 按更新通道升级 DSH
func Upgrade() {
	state.SetStatus(StatusBuilding, "正在准备更新核心服务...")
	go update()
}

// DeployVersion 按指定适配版本在线部署 DSH。
func DeployVersion(targetVersion string) {
	state.SetStatus(StatusBuilding, "正在准备在线部署核心服务...")
	go deployVersion(targetVersion, true, false)
}

// RepairCurrent 保留数据并干净重装当前 DSH 版本。
func RepairCurrent(keepPlugins bool) {
	state.SetStatus(StatusBuilding, "正在准备重置修复当前版本...")
	go func() {
		targetVersion := strings.TrimPrefix(strings.TrimSpace(GetConfig().Version), "v")
		if targetVersion == "" || targetVersion == "-" {
			targetVersion = strings.TrimPrefix(strings.TrimSpace(readVersion()), "v")
		}
		deployVersion(targetVersion, keepPlugins, true)
	}()
}

// safeRemoveAll 安全递归删除文件与目录，遇到只读权限文件时自动解除只读以彻底清理
func safeRemoveAll(path string) error {
	if err := os.RemoveAll(path); err == nil {
		return nil
	}
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0777)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// installDshFromNpm 通过 npm 安装指定版本的 DSH 运行时
func installDshFromNpm(targetVersion string) error {
	_ = os.MkdirAll(runtimeDir, 0755)

	targetVersion = strings.TrimPrefix(strings.TrimSpace(targetVersion), "v")
	if targetVersion == "" {
		return fmt.Errorf("目标版本不能为空")
	}
	pkgSpec := fmt.Sprintf("%s@%s", dshPackageName, targetVersion)

	cfg := GetConfig()
	args := []string{
		"install",
		pkgSpec,
		"--dangerously-allow-all-scripts",
		"--no-audit",
		"--no-fund",
		"--registry=" + cfg.GetNpmRegistry(),
	}
	if globalNpmCache != "" {
		_ = os.MkdirAll(globalNpmCache, 0755)
		args = append(args, "--cache="+globalNpmCache)
	}

	LogInfo("[升级] 执行包安装命令: npm %s", strings.Join(args, " "))
	cmd := exec.Command(npmBin(), args...)
	cmd.Dir = runtimeDir
	cmd.Stdout = NewLogWriter()
	cmd.Stderr = NewLogWriter()

	cmdEnv := append([]string{}, os.Environ()...)
	if cfg.NetworkProxy != "" && strings.Contains(cfg.GetNpmRegistry(), "npmjs.org") {
		cmdEnv = append(cmdEnv,
			"HTTP_PROXY="+cfg.NetworkProxy,
			"HTTPS_PROXY="+cfg.NetworkProxy,
			"http_proxy="+cfg.NetworkProxy,
			"https_proxy="+cfg.NetworkProxy,
		)
	}
	cmd.Env = cmdEnv

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install 失败: %w", err)
	}

	// 校验安装结果
	cliBinJs := filepath.Join(runtimeDir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	if _, err := os.Stat(cliBinJs); err != nil {
		return fmt.Errorf("npm 安装完成但入口文件缺失: %s", cliBinJs)
	}

	LogInfo("[升级] 核心运行时包安装成功: %s", pkgSpec)
	go installPnpm()
	return nil
}

func validateDeployVersion(targetVersion string) error {
	if _, ok := parseSemver(targetVersion); !ok {
		return fmt.Errorf("目标版本格式无效")
	}
	if CompareSemver(targetVersion, minDshVersion) < 0 {
		return fmt.Errorf("最低支持 v%s", minDshVersion)
	}

	info, err := fetchRemoteNpmInfo(dshPackageName)
	if err != nil {
		return err
	}
	for _, version := range collectAdaptedVersions(info) {
		if version == targetVersion {
			currentVersion := strings.TrimPrefix(strings.TrimSpace(readVersion()), "v")
			if _, ok := parseSemver(currentVersion); ok && CompareSemver(targetVersion, currentVersion) < 0 {
				return fmt.Errorf("不允许降级 (当前 v%s)", currentVersion)
			}
			return nil
		}
	}
	return fmt.Errorf("v%s 不存在或未适配", targetVersion)
}

func deployVersion(targetVersion string, keepPlugins bool, isClean bool) {
	targetVersion = strings.TrimPrefix(strings.TrimSpace(targetVersion), "v")
	if err := validateDeployVersion(targetVersion); err != nil {
		state.SetStatus(StatusStopped, "部署失败: "+err.Error())
		return
	}

	stopAndWait()
	if !keepPlugins {
		ResetAllProfilePatches()
	}

	if isClean {
		state.SetStatus(StatusBuilding, fmt.Sprintf("正在重置修复核心服务 (v%s)...", targetVersion))
	} else {
		state.SetStatus(StatusBuilding, fmt.Sprintf("正在在线部署核心服务 (v%s)...", targetVersion))
	}
	state.SetTargetVersion(targetVersion)
	if isClean {
		_ = safeRemoveAll(runtimeDir)
	} else {
		_ = safeRemoveAll(filepath.Join(runtimeDir, "node_modules"))
	}

	if err := installDshFromNpm(targetVersion); err != nil {
		LogWarning("[升级] 部署失败: %s", err)
		state.SetStatus(StatusStopped, "部署失败: "+err.Error())
		return
	}

	verAfter := readVersion()
	LogInfo("[升级] 核心服务部署成功: v%s", verAfter)
	refreshVersion()
	SetBuildTime(time.Now())
	state.SetStatus(StatusStopped, "")
	restartService()
}

func update() {
	stopAndWait()

	// 检查更新与在线升级：检测 NPM 上游最新发布版本
	info, err := fetchRemoteNpmInfo(dshPackageName)
	if err != nil || info == nil {
		errMsg := "获取远程版本信息失败"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		LogWarning("[升级] %s", errMsg)
		state.SetStatus(StatusStopped, errMsg)
		restartService()
		return
	}

	channel := GetConfig().GetUpdateChannel()
	targetVer := resolveTargetVersion(info, channel)
	if targetVer == "" {
		LogWarning("[升级] 解析远程版本失败，无有效语义化版本")
		state.SetStatus(StatusStopped, "解析远程版本失败")
		restartService()
		return
	}

	verBefore := strings.TrimPrefix(strings.TrimSpace(GetConfig().Version), "v")
	if verBefore == "" || verBefore == "-" {
		verBefore = readVersion()
	}

	if verBefore != "" && CompareSemver(targetVer, verBefore) <= 0 {
		LogInfo("[升级] 当前运行版本 (v%s) 已高于或等于远端目标版本 (v%s)，跳过更新", verBefore, targetVer)
		state.SetStatus(StatusStopped, "")
		restartService()
		return
	}

	state.SetStatus(StatusBuilding, fmt.Sprintf("正在更新核心服务 (v%s → v%s)...", verBefore, targetVer))
	state.SetTargetVersion(targetVer)

	if err := installDshFromNpm(targetVer); err != nil {
		LogWarning("[升级] 安装更新失败: %s", err)
		state.SetStatus(StatusStopped, "更新安装失败: "+err.Error())
		return
	}

	verAfter := readVersion()
	LogInfo("[升级] 核心服务更新成功: v%s", verAfter)
	refreshVersion()
	SetBuildTime(time.Now())
	state.SetStatus(StatusStopped, "")
	restartService()
}

func extractTarGz(tarPath, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	cmd := exec.Command("tar", "--no-same-owner", "-xzf", tarPath, "-C", dst)
	cmd.Stdout = NewLogWriter()
	cmd.Stderr = NewLogWriter()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("解压 tar.gz 失败: %w", err)
	}
	return nil
}

func refreshVersion() {
	ver := readVersion()
	if ver == "" {
		ver = "-"
	}
	SetVersion(ver)
}

func readVersion() string {
	data, err := os.ReadFile(filepath.Join(runtimeDir, "node_modules", "@deepseek-ai", "dsh", "package.json"))
	if err != nil {
		// 备用：从运行时顶层 package.json 读取依赖版本
		if pdata, err := os.ReadFile(filepath.Join(runtimeDir, "package.json")); err == nil {
			var pkg struct {
				Dependencies map[string]string `json:"dependencies"`
			}
			if json.Unmarshal(pdata, &pkg) == nil && pkg.Dependencies != nil {
				return strings.TrimLeft(pkg.Dependencies[dshPackageName], "^~v ")
			}
		}
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
