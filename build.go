package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

func Upgrade() {
	state.SetStatus(StatusBuilding, "正在准备更新...")
	go update(false)
}

func Rebuild() {
	state.SetStatus(StatusBuilding, "正在准备强制重建...")
	go update(true)
}

func update(forceRebuild bool) {
	stopAndWait()

	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		if forceRebuild {
			state.SetStatus(StatusStopped, "源码不存在，请先拉取更新")
			return
		}
		state.SetStatus(StatusBuilding, "正在检查远程更新...")
		if err := gitClone(); err != nil {
			LogWarning("Git 克隆失败: %s", err)
			state.SetStatus(StatusStopped, "克隆失败: "+err.Error())
			return
		}
		if err := buildSource(false); err != nil {
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
	} else {
		commitBefore := gitHead()
		state.SetStatus(StatusBuilding, "正在拉取远程更新...")
		if err := gitPull(); err != nil {
			LogWarning("Git 拉取失败: %s", err)
			state.SetStatus(StatusStopped, "git pull 失败: "+err.Error())
			return
		}
		commitAfter := gitHead()
		if commitBefore != "" && commitBefore == commitAfter {
			LogInfo("当前已是最新版本 (%s)，跳过构建", commitAfter)
			state.SetStatus(StatusBuilding, fmt.Sprintf("当前已是最新版本 (%s)，跳过构建", commitAfter))
			refreshCommit()
			restartService()
			return
		}
		shortTarget := commitAfter
		if len(shortTarget) > 7 {
			shortTarget = shortTarget[:7]
		}
		state.SetTargetCommit(shortTarget)
		LogInfo("检测到版本变更 (%s → %s)，开始构建", commitBefore, commitAfter)
		state.SetStatus(StatusBuilding, fmt.Sprintf("检测到版本变更 (%s → %s)，正在同步依赖与构建...", commitBefore, commitAfter))
	}

	if err := buildSource(false); err != nil {
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
	return runCmd(pnpmDir, npmBin(), "install", "pnpm")
}

func ensureGCC() error {
	if _, err := exec.LookPath("gcc"); err == nil {
		return nil
	}
	state.SetStatus(StatusBuilding, "缺少构建工具，正在安装 gcc...")
	if err := installGCC(); err != nil {
		return fmt.Errorf("自动安装 gcc 失败: %w\n请手动安装 gcc/g++ 后重试", err)
	}
	if _, err := exec.LookPath("gcc"); err != nil {
		return fmt.Errorf("gcc 安装后仍未检测到，请手动安装")
	}
	LogInfo("gcc 工具链安装完成")
	return nil
}

func installGCC() error {
	args := []string{"install", "-y", "build-essential"}
	LogInfo("缺失 gcc 工具链，正在执行 apt-get install build-essential")
	cmd := exec.Command("apt-get", args...)
	cmd.Stdout = NewLogWriterInfo()
	cmd.Stderr = NewLogWriterWarn()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apt-get install build-essential 失败: %w", err)
	}
	return nil
}

func landlockNativeDir() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return filepath.Join(srcDir, "native", "landlock-run", "packages", "linux-"+arch)
}

func ensureMusl() error {
	if _, err := exec.LookPath("musl-gcc"); err == nil {
		return nil
	}
	state.SetStatus(StatusBuilding, "缺少 musl 工具链，正在安装 musl-tools...")
	LogInfo("缺失 musl 工具链，正在执行 apt-get install musl-tools")
	cmd := exec.Command("apt-get", "install", "-y", "musl-tools")
	cmd.Stdout = NewLogWriterInfo()
	cmd.Stderr = NewLogWriterWarn()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apt-get install musl-tools 失败: %w", err)
	}
	if _, err := exec.LookPath("musl-gcc"); err != nil {
		return fmt.Errorf("musl-tools 安装后仍未检测到 musl-gcc，请手动安装")
	}
	LogInfo("musl-tools 工具链安装完成")
	return nil
}

func landlockBinPath() string {
	return filepath.Join(landlockNativeDir(), "bin", "landlock-run")
}

func buildLandlock() error {
	state.SetStatus(StatusBuilding, "正在构建 landlock 沙箱组件...")
	LogInfo("开始编译 landlock 原生沙箱组件")
	if err := ensureMusl(); err != nil {
		return err
	}
	landlockDir := filepath.Join(srcDir, "native", "landlock-run")
	if _, err := os.Stat(landlockDir); err == nil {
		if err := runCmd(landlockDir, pnpmBin(), "run", "build:native"); err == nil {
			bin := landlockBinPath()
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
	LogInfo("landlock 原生沙箱组件构建完成: %s", bin)
	return nil
}

func hasPrebuiltArtifacts() bool {
	webIndex := filepath.Join(srcDir, "apps", "web", "dist", "index.html")
	if _, err := os.Stat(webIndex); err != nil {
		return false
	}
	coreLib := filepath.Join(srcDir, "packages", "api", "remotes", "lib")
	if _, err := os.Stat(coreLib); err != nil {
		return false
	}
	if _, err := os.Stat(landlockBinPath()); err != nil {
		return false
	}
	return true
}

func hasNodeModules() bool {
	info, err := os.Stat(filepath.Join(srcDir, "node_modules"))
	return err == nil && info.IsDir()
}

func buildSource(allowFastStart bool) error {
	state.SetStatus(StatusBuilding, "正在安装 pnpm...")
	if err := installPnpm(); err != nil {
		return fmt.Errorf("install pnpm: %w", err)
	}

	prebuilt := hasPrebuiltArtifacts()
	hasModules := hasNodeModules()
	isPrebuilt := isPrebuiltPkg()

	// 预构建包极速启动：产物完备直接跳过编译
	if allowFastStart && isPrebuilt && hasModules && prebuilt {
		state.SetStatus(StatusBuilding, "检测到预构建产物，正在极速启动...")
		LogInfo("检测到内置离线预构建源码包（产物与依赖完备），跳过依赖拉取与项目编译，极速启动")
	} else {
		if allowFastStart && (!isPrebuilt || !prebuilt || !hasModules) {
			LogInfo("检测到内置离线源码包，开始自动配置编译环境并安装依赖")
		}
		if err := ensureGCC(); err != nil {
			return err
		}
		state.SetStatus(StatusBuilding, "正在安装依赖...")
		if err := runCmd(srcDir, pnpmBin(), "install", "--prefer-offline", "--config.confirm-modules-purge=false", "--registry", "https://registry.npmmirror.com"); err != nil {
			return fmt.Errorf("pnpm install: %w", err)
		}
		state.SetStatus(StatusBuilding, "正在编译构建...")
		if err := runCmd(srcDir, pnpmBin(), "run", "build"); err != nil {
			return fmt.Errorf("pnpm run build: %w", err)
		}
		LogInfo("项目源码编译完成")

		if err := buildLandlock(); err != nil {
			return err
		}
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

func isPrebuiltPkg() bool {
	data, err := os.ReadFile(filepath.Join(appDest, ".pkg_type"))
	if err == nil && strings.TrimSpace(string(data)) == "prebuilt" {
		return true
	}
	return false
}

func pkgTypeName() string {
	if isPrebuiltPkg() {
		return "内置离线预构建源码包"
	}
	return "内置离线源码包"
}

// compareSemver 比较语义化版本，返回 1 (v1>v2), -1 (v1<v2), 0 (相等)
func compareSemver(v1, v2 string) int {
	v1 = strings.TrimPrefix(strings.TrimSpace(v1), "v")
	v2 = strings.TrimPrefix(strings.TrimSpace(v2), "v")
	if v1 == v2 {
		return 0
	}
	if v1 == "" {
		return -1
	}
	if v2 == "" {
		return 1
	}

	splitVer := func(v string) (core string, pre string) {
		if idx := strings.Index(v, "-"); idx != -1 {
			return v[:idx], v[idx+1:]
		}
		return v, ""
	}

	core1, pre1 := splitVer(v1)
	core2, pre2 := splitVer(v2)

	parseNums := func(s string) []int {
		parts := strings.Split(s, ".")
		nums := make([]int, 0, len(parts))
		for _, p := range parts {
			var n int
			for _, r := range p {
				if r >= '0' && r <= '9' {
					n = n*10 + int(r-'0')
				} else {
					break
				}
			}
			nums = append(nums, n)
		}
		return nums
	}

	nums1, nums2 := parseNums(core1), parseNums(core2)
	maxLen := len(nums1)
	if len(nums2) > maxLen {
		maxLen = len(nums2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(nums1) {
			n1 = nums1[i]
		}
		if i < len(nums2) {
			n2 = nums2[i]
		}
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}

	if pre1 == "" && pre2 != "" {
		return 1
	}
	if pre1 != "" && pre2 == "" {
		return -1
	}
	if pre1 != "" && pre2 != "" {
		parts1 := strings.Split(pre1, ".")
		parts2 := strings.Split(pre2, ".")
		maxParts := len(parts1)
		if len(parts2) > maxParts {
			maxParts = len(parts2)
		}

		for i := 0; i < maxParts; i++ {
			if i >= len(parts1) {
				return -1
			}
			if i >= len(parts2) {
				return 1
			}

			p1, p2 := parts1[i], parts2[i]
			if p1 == p2 {
				continue
			}

			n1, err1 := strconv.Atoi(p1)
			n2, err2 := strconv.Atoi(p2)

			if err1 == nil && err2 == nil {
				if n1 > n2 {
					return 1
				}
				if n1 < n2 {
					return -1
				}
			} else if err1 == nil && err2 != nil {
				return -1
			} else if err1 != nil && err2 == nil {
				return 1
			} else {
				if p1 > p2 {
					return 1
				}
				if p1 < p2 {
					return -1
				}
			}
		}
	}
	return 0
}

