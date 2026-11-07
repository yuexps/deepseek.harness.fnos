package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	DefaultNpmRegistry  = "https://registry.npmmirror.com"
	OfficialNpmRegistry = "https://registry.npmjs.org"
)

type Config struct {
	ServerPort      int    `json:"server_port"`
	ProxyPort       int    `json:"proxy_port"`
	HeapMemoryLimit int    `json:"heap_memory_limit,omitempty"`
	NetworkProxy    string `json:"network_proxy"`
	ProxyDshRuntime *bool  `json:"proxy_dsh_runtime,omitempty"`
	NpmRegistry     string `json:"npm_registry,omitempty"`
	UpdateChannel   string `json:"update_channel,omitempty"`
	AccessMode      string `json:"access_mode,omitempty"`
	ReverseProxyURL string `json:"reverse_proxy_url,omitempty"`
	AccessPassword  string `json:"access_password,omitempty"`
	DataLibraryPath string `json:"data_library_path,omitempty"`
	Version         string `json:"version,omitempty"`
	BuildTime       string `json:"build_time,omitempty"`
	LastRunState    string `json:"last_run_state,omitempty"`
}

func (c Config) IsProxyDshRuntimeEnabled() bool {
	if c.ProxyDshRuntime == nil {
		return false
	}
	return *c.ProxyDshRuntime
}

func (c Config) GetServerPort() int {
	if c.ServerPort <= 0 {
		return 2298
	}
	return c.ServerPort
}

func (c Config) GetProxyPort() int {
	if c.ProxyPort <= 0 {
		return 2299
	}
	return c.ProxyPort
}

func (c Config) GetNpmRegistry() string {
	if trimmed := strings.TrimSpace(c.NpmRegistry); trimmed != "" {
		return trimmed
	}
	return DefaultNpmRegistry
}

func (c Config) GetUpdateChannel() string {
	channel := strings.ToLower(strings.TrimSpace(c.UpdateChannel))
	switch channel {
	case "latest", "alpha", "next", "newest":
		return channel
	case "stable":
		return "latest"
	default:
		return "latest"
	}
}

var (
	globalConfig   Config
	configMu       sync.RWMutex
	configFilePath string
)

func InitConfig() {
	configFilePath = filepath.Join(globalPkgVar, "config.json")
	defaultProxyDsh := false
	globalConfig = Config{
		ServerPort:      2298,
		ProxyPort:       2299,
		AccessMode:      "fngateway",
		DataLibraryPath: globalPkgVar,
		ProxyDshRuntime: &defaultProxyDsh,
		NpmRegistry:     DefaultNpmRegistry,
		UpdateChannel:   "latest",
	}
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &globalConfig)
	globalConfig.UpdateChannel = globalConfig.GetUpdateChannel()
	if globalConfig.ProxyDshRuntime == nil {
		globalConfig.ProxyDshRuntime = &defaultProxyDsh
	}
	if globalConfig.NpmRegistry == "" {
		globalConfig.NpmRegistry = DefaultNpmRegistry
	}
	if globalConfig.AccessMode == "" {
		if globalConfig.ReverseProxyURL != "" {
			globalConfig.AccessMode = "custom"
		} else {
			globalConfig.AccessMode = "fngateway"
		}
	}
	globalConfig.DataLibraryPath = globalPkgVar
}

// InitAppEnv 初始化全局环境变量供子进程继承
func InitAppEnv() {
	pnpmBinDir := filepath.Join(globalPnpmDir, "node_modules", ".bin")
	_ = os.Setenv("PATH", pnpmBinDir+":"+nodeBinDir+":/bin:/usr/bin:"+os.Getenv("PATH"))
	_ = os.MkdirAll(globalHomeDir, 0755)
	_ = os.Setenv("HOME", globalHomeDir)
	_ = os.Setenv("CI", "true")
	// 声明远程环境：触发 DSH 原生远程适配
	_ = os.Setenv("SSH_CONNECTION", "127.0.0.1 0 127.0.0.1 22")

	storeDir := filepath.Join(globalPnpmHome, "store")
	_ = os.Setenv("PNPM_HOME", globalPnpmHome)
	_ = os.Setenv("pnpm_config_store_dir", storeDir)
	_ = os.Setenv("npm_config_cache", globalNpmCache)

	ApplyNpmRegistryEnv()

	_ = os.Setenv("DSH_HOME", globalDshHome)
	_ = os.Setenv("DSH_AGENTS_HOME", filepath.Join(globalDshHome, "agents"))

	ApplyProxyEnv()
}

// EnsureDshEnvFile 确保 DSH home 的环境变量文件存在，返回其绝对路径
func EnsureDshEnvFile() (string, error) {
	if err := os.MkdirAll(globalDshHome, 0755); err != nil {
		return "", err
	}
	envPath := filepath.Join(globalDshHome, ".env")
	if _, err := os.Stat(envPath); err == nil {
		return envPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	file, err := os.OpenFile(envPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_ = file.Close()
	LogInfo("[环境变量] 文件不存在，已自动已创建: %s", envPath)
	return envPath, nil
}

// EnvVarItem 环境变量文件中的一条键值对
type EnvVarItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnvNames 仅允许由启动环境设置的变量名，写入 .env 会使 DSH 启动失败
var reservedEnvNames = map[string]struct{}{
	"PATH": {}, "HOME": {}, "USERPROFILE": {}, "SHELL": {},
	"NODE_OPTIONS": {}, "NODE_PATH": {}, "NODE_EXTRA_CA_CERTS": {},
	"LD_PRELOAD": {}, "LD_LIBRARY_PATH": {}, "LD_AUDIT": {},
	"BASH_ENV": {}, "ENV": {}, "SHELLOPTS": {}, "BASHOPTS": {},
	"PERL5OPT": {}, "PERL5LIB": {}, "PYTHONSTARTUP": {}, "PYTHONPATH": {},
	"RUBYOPT": {}, "RUBYLIB": {},
	"JAVA_TOOL_OPTIONS": {}, "_JAVA_OPTIONS": {}, "JDK_JAVA_OPTIONS": {}, "PYTHONHOME": {},
	"GIT_SSH": {}, "GIT_SSH_COMMAND": {}, "GIT_EXTERNAL_DIFF": {}, "GIT_PAGER": {},
	"GIT_EDITOR": {}, "GIT_ASKPASS": {}, "SSH_ASKPASS": {},
	"GIT_CONFIG_GLOBAL": {}, "GIT_CONFIG_SYSTEM": {}, "GIT_CONFIG_COUNT": {},
	"EDITOR": {}, "VISUAL": {}, "PAGER": {}, "BROWSER": {},
	"DEEPSEEK_BASE_URL": {}, "DEEPSEEK_SEARCH_BASE_URL": {},
	"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {},
	"REQUESTS_CA_BUNDLE": {}, "CURL_CA_BUNDLE": {}, "NODE_TLS_REJECT_UNAUTHORIZED": {},
}

// reservedEnvPrefixes 仅允许由启动环境设置的变量名前缀
var reservedEnvPrefixes = []string{"DSH_", "XDG_", "DYLD_", "BASH_FUNC_"}

// homeProxyEnvNames 环境变量文件所在层允许设置的代理变量
var homeProxyEnvNames = map[string]struct{}{
	"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "ALL_PROXY": {}, "NO_PROXY": {},
}

// isReservedEnvName 判断变量名是否只能由启动环境设置
func isReservedEnvName(name string) bool {
	upper := strings.ToUpper(name)
	if _, isProxy := homeProxyEnvNames[upper]; isProxy {
		return false
	}
	if _, isReserved := reservedEnvNames[upper]; isReserved {
		return true
	}
	for _, prefix := range reservedEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// ValidateEnvVars 校验环境变量键值对合法性
func ValidateEnvVars(items []EnvVarItem) error {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			return fmt.Errorf("变量名不能为空")
		}
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("变量名「%s」不合法，仅允许字母、数字与下划线，且不能以数字开头", item.Key)
		}
		if isReservedEnvName(key) {
			return fmt.Errorf("变量名「%s」只能由服务启动环境设置，写入 .env 会导致 DSH 启动失败", key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("变量名「%s」重复", key)
		}
		seen[key] = struct{}{}
		if strings.ContainsAny(item.Value, "\r\n") {
			return fmt.Errorf("变量「%s」的值不能包含换行", key)
		}
	}
	return nil
}

// ReadDshEnvVars 读取环境变量文件中的键值对
func ReadDshEnvVars() ([]EnvVarItem, error) {
	envPath, err := EnsureDshEnvFile()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		return nil, err
	}
	items, _ := parseDshEnv(string(raw))
	return items, nil
}

// WriteDshEnvVars 覆盖写入环境变量文件，顶部原样保留注释与无法识别的行
func WriteDshEnvVars(items []EnvVarItem) error {
	envPath, err := EnsureDshEnvFile()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}
	_, preserved := parseDshEnv(string(raw))

	lines := make([]string, 0, len(preserved)+len(items)+1)
	lines = append(lines, preserved...)
	if len(preserved) > 0 && len(items) > 0 {
		lines = append(lines, "")
	}
	for _, item := range items {
		lines = append(lines, strings.TrimSpace(item.Key)+"="+quoteEnvValue(item.Value))
	}

	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(envPath, []byte(content), 0600)
}

// parseDshEnv 解析环境变量文本，返回键值对与需原样保留的行
func parseDshEnv(raw string) ([]EnvVarItem, []string) {
	items := make([]EnvVarItem, 0, 8)
	preserved := make([]string, 0, 4)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			preserved = append(preserved, line)
			continue
		}
		if rest, found := strings.CutPrefix(line, "export "); found {
			line = strings.TrimSpace(rest)
		}
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || !envKeyPattern.MatchString(key) {
			preserved = append(preserved, line)
			continue
		}
		items = append(items, EnvVarItem{Key: key, Value: unquoteEnvValue(strings.TrimSpace(value))})
	}
	return items, preserved
}

// quoteEnvValue 对含特殊字符的值加双引号并转义，避免被解析为注释或截断
func quoteEnvValue(value string) string {
	if value == "" {
		return ""
	}
	if !strings.ContainsAny(value, "\"'#\\") && value == strings.TrimSpace(value) {
		return value
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// unquoteEnvValue 剥离成对引号并还原转义字符
func unquoteEnvValue(value string) string {
	if len(value) < 2 {
		return value
	}
	if value[0] == '"' && value[len(value)-1] == '"' {
		inner := value[1 : len(value)-1]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		return strings.ReplaceAll(inner, `\\`, `\`)
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}

// ApplyNpmRegistryEnv 根据当前配置应用 NPM 依赖源环境变量并同步 .npmrc
func ApplyNpmRegistryEnv() {
	registry := GetConfig().GetNpmRegistry()
	_ = os.Setenv("npm_config_registry", registry)
	_ = os.Setenv("NPM_CONFIG_REGISTRY", registry)
	_ = os.Setenv("pnpm_config_registry", registry)
	_ = os.Setenv("PNPM_CONFIG_REGISTRY", registry)

	if globalHomeDir != "" {
		homeNpmrc := filepath.Join(globalHomeDir, ".npmrc")
		_ = os.MkdirAll(filepath.Dir(homeNpmrc), 0755)
		_ = os.WriteFile(homeNpmrc, []byte("registry="+registry+"\n"), 0644)
	}
}

// ApplyProxyEnv 根据当前配置应用或清理网络代理环境变量
func ApplyProxyEnv() {
	cfg := GetConfig()
	if cfg.NetworkProxy != "" && cfg.IsProxyDshRuntimeEnabled() {
		noProxy := "localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,*.local,registry.npmmirror.com,npmmirror.com"
		if u, err := url.Parse(cfg.GetNpmRegistry()); err == nil && u.Hostname() != "" {
			if !strings.Contains(u.Hostname(), "npmjs.org") && !strings.Contains(noProxy, u.Hostname()) {
				noProxy += "," + u.Hostname()
			}
		}
		_ = os.Setenv("npm_config_proxy", cfg.NetworkProxy)
		_ = os.Setenv("npm_config_https_proxy", cfg.NetworkProxy)
		_ = os.Setenv("npm_config_noproxy", noProxy)
		_ = os.Setenv("HTTP_PROXY", cfg.NetworkProxy)
		_ = os.Setenv("HTTPS_PROXY", cfg.NetworkProxy)
		_ = os.Setenv("ALL_PROXY", cfg.NetworkProxy)
		_ = os.Setenv("http_proxy", cfg.NetworkProxy)
		_ = os.Setenv("https_proxy", cfg.NetworkProxy)
		_ = os.Setenv("all_proxy", cfg.NetworkProxy)
		_ = os.Setenv("NO_PROXY", noProxy)
		_ = os.Setenv("no_proxy", noProxy)
		_ = os.Setenv("NODE_USE_ENV_PROXY", "1")
	} else {
		_ = os.Unsetenv("npm_config_proxy")
		_ = os.Unsetenv("npm_config_https_proxy")
		_ = os.Unsetenv("npm_config_noproxy")
		_ = os.Unsetenv("HTTP_PROXY")
		_ = os.Unsetenv("HTTPS_PROXY")
		_ = os.Unsetenv("ALL_PROXY")
		_ = os.Unsetenv("http_proxy")
		_ = os.Unsetenv("https_proxy")
		_ = os.Unsetenv("all_proxy")
		_ = os.Unsetenv("NO_PROXY")
		_ = os.Unsetenv("no_proxy")
		_ = os.Unsetenv("NODE_USE_ENV_PROXY")
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
	ApplyNpmRegistryEnv()
	return nil
}
