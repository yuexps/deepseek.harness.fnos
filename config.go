package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	ServerPort         int    `json:"server_port"`
	ProxyPort          int    `json:"proxy_port"`
	HeapMemoryLimit    int    `json:"heap_memory_limit,omitempty"`
	NetworkProxy       string `json:"network_proxy"`
	AccessMode         string `json:"access_mode,omitempty"`
	ReverseProxyURL    string `json:"reverse_proxy_url,omitempty"`
	AccessPassword     string `json:"access_password,omitempty"`
	DataLibraryPath    string `json:"data_library_path,omitempty"`
	EnableBuiltinSkill *bool  `json:"enable_builtin_skill,omitempty"`
	Version            string `json:"version,omitempty"`
	Commit             string `json:"commit,omitempty"`
	BuildTime          string `json:"build_time,omitempty"`
	LastRunState       string `json:"last_run_state,omitempty"`
}

var (
	globalConfig   Config
	configMu       sync.RWMutex
	configFilePath string
)

func InitConfig(pkgVar string) {
	configFilePath = filepath.Join(pkgVar, "config.json")
	defaultSkillEnabled := true
	globalConfig = Config{
		ServerPort:         2298,
		ProxyPort:          2299,
		AccessMode:         "fngateway",
		DataLibraryPath:    pkgVar,
		EnableBuiltinSkill: &defaultSkillEnabled,
	}
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &globalConfig)
	if globalConfig.EnableBuiltinSkill == nil {
		globalConfig.EnableBuiltinSkill = &defaultSkillEnabled
	}
	if globalConfig.AccessMode == "" {
		if globalConfig.ReverseProxyURL != "" {
			globalConfig.AccessMode = "custom"
		} else {
			globalConfig.AccessMode = "fngateway"
		}
	}
	globalConfig.DataLibraryPath = pkgVar
}

// InitAppEnv 初始化全局环境变量供子进程继承
func InitAppEnv(pkgVar string) {
	pnpmBinDir := filepath.Join(pkgVar, "pnpm-env", "node_modules", ".bin")
	_ = os.Setenv("PATH", pnpmBinDir+":"+nodeBinDir+":/bin:/usr/bin:"+os.Getenv("PATH"))
	homeDir := filepath.Join(pkgVar, "home")
	_ = os.MkdirAll(homeDir, 0755)
	_ = os.Setenv("HOME", homeDir)
	_ = os.Setenv("CI", "true")

	pnpmHome := filepath.Join(pkgVar, "pnpm-home")
	storeDir := filepath.Join(pnpmHome, "store")
	_ = os.Setenv("PNPM_HOME", pnpmHome)
	_ = os.Setenv("pnpm_config_store_dir", storeDir)
	_ = os.Setenv("npm_config_cache", filepath.Join(pkgVar, "npm-cache"))
	_ = os.Setenv("npm_config_registry", "https://registry.npmmirror.com")
	_ = os.Setenv("NPM_CONFIG_REGISTRY", "https://registry.npmmirror.com")
	_ = os.Setenv("pnpm_config_registry", "https://registry.npmmirror.com")
	_ = os.Setenv("PNPM_CONFIG_REGISTRY", "https://registry.npmmirror.com")

	// 固化全局 .npmrc 确保 pnpm 11 及子进程稳定使用国内镜像
	homeNpmrc := filepath.Join(homeDir, ".npmrc")
	if _, err := os.Stat(homeNpmrc); os.IsNotExist(err) {
		_ = os.WriteFile(homeNpmrc, []byte("registry=https://registry.npmmirror.com\n"), 0644)
	}

	dshHome := filepath.Join(pkgVar, "dsh-data")
	_ = os.Setenv("DSH_HOME", dshHome)
	_ = os.Setenv("DSH_AGENTS_HOME", filepath.Join(dshHome, "agents"))

	ApplyProxyEnv()
}

// ApplyProxyEnv 根据当前配置应用或清理网络代理环境变量
func ApplyProxyEnv() {
	cfg := GetConfig()
	if cfg.NetworkProxy != "" {
		noProxy := "localhost,127.0.0.1,::1,registry.npmmirror.com,npmmirror.com"
		_ = os.Setenv("npm_config_proxy", cfg.NetworkProxy)
		_ = os.Setenv("npm_config_https_proxy", cfg.NetworkProxy)
		_ = os.Setenv("npm_config_noproxy", noProxy)
		_ = os.Setenv("HTTP_PROXY", cfg.NetworkProxy)
		_ = os.Setenv("HTTPS_PROXY", cfg.NetworkProxy)
		_ = os.Setenv("ALL_PROXY", cfg.NetworkProxy)
		_ = os.Setenv("NO_PROXY", noProxy)
		_ = os.Setenv("no_proxy", noProxy)
	} else {
		_ = os.Unsetenv("npm_config_proxy")
		_ = os.Unsetenv("npm_config_https_proxy")
		_ = os.Unsetenv("npm_config_noproxy")
		_ = os.Unsetenv("HTTP_PROXY")
		_ = os.Unsetenv("HTTPS_PROXY")
		_ = os.Unsetenv("ALL_PROXY")
		_ = os.Unsetenv("NO_PROXY")
		_ = os.Unsetenv("no_proxy")
	}
}

func GetConfig() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}

func GetBuildTime() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.BuildTime
}

func GetVersion() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.Version
}

func GetCommit() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.Commit
}

func GetLastRunState() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.LastRunState
}

func persistConfig() {
	data, err := json.MarshalIndent(globalConfig, "", "  ")
	if err != nil {
		return
	}
	tmpFile := configFilePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmpFile, configFilePath)
}

func SetBuildTime(t time.Time) {
	configMu.Lock()
	globalConfig.BuildTime = t.Format("2006-01-02 15:04")
	configMu.Unlock()
	persistConfig()
}

func SetVersion(v string) {
	configMu.Lock()
	globalConfig.Version = v
	configMu.Unlock()
	persistConfig()
}

func SetCommit(c string) {
	configMu.Lock()
	globalConfig.Commit = c
	configMu.Unlock()
	persistConfig()
}

func SetLastRunState(st string) {
	configMu.Lock()
	globalConfig.LastRunState = st
	configMu.Unlock()
	persistConfig()
}

func SaveConfig(cfg Config) error {
	configMu.Lock()
	if cfg.LastRunState == "" && globalConfig.LastRunState != "" {
		cfg.LastRunState = globalConfig.LastRunState
	}
	globalConfig = cfg
	configMu.Unlock()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpFile := configFilePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpFile, configFilePath); err != nil {
		return err
	}
	ApplyProxyEnv()
	ApplyBuiltinSkillConfig(globalPkgVar, appDest)
	return nil
}

// ApplyBuiltinSkillConfig 同步或切换内置飞牛官方 TRIM CLI 技能文件
func ApplyBuiltinSkillConfig(pkgVar, appdest string) {
	if pkgVar == "" {
		pkgVar = globalPkgVar
	}
	if appdest == "" {
		appdest = os.Getenv("TRIM_APPDEST")
	}

	skillSrc := filepath.Join(appdest, "bin", "skill")
	if _, err := os.Stat(skillSrc); err != nil {
		skillSrc = "fnpack/app/bin/skill"
	}
	if _, err := os.Stat(skillSrc); err != nil {
		return
	}

	cfg := GetConfig()
	enabled := true
	if cfg.EnableBuiltinSkill != nil {
		enabled = *cfg.EnableBuiltinSkill
	}

	skillsDir := filepath.Join(pkgVar, "dsh-data", "skills")
	targetSkill := filepath.Join(skillsDir, "trim-cli")

	if !enabled {
		_ = os.RemoveAll(targetSkill)
		LogInfo("[内置技能] 飞牛官方 TRIM CLI 技能已禁用并移除")
		return
	}

	_ = os.MkdirAll(skillsDir, 0755)

	// 复制技能目录，避免沙箱软链接隔离权限异常
	_ = os.RemoveAll(targetSkill)
	if err := copyDir(skillSrc, targetSkill); err != nil {
		LogError("[内置技能] 复制技能文件失败: %v", err)
		return
	}

	// 设置脚本与二进制文件可执行权限
	_ = os.Chmod(filepath.Join(targetSkill, "scripts", "trim-cli"), 0755)
	if binFiles, err := os.ReadDir(filepath.Join(targetSkill, "bin")); err == nil {
		for _, f := range binFiles {
			_ = os.Chmod(filepath.Join(targetSkill, "bin", f.Name()), 0755)
		}
	}

	LogInfo("[内置技能] 飞牛官方 TRIM CLI 技能已就绪 (目标: %s)", targetSkill)
}

// copyFile 复制单个文件并保留权限
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// copyDir 递归复制目录
func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}
