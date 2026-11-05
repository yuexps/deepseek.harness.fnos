package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	pluginRemoveTimeout  = 300 * time.Second
	pluginInstallTimeout = 600 * time.Second
)

// pluginEnv 注入网络超时与重试收敛参数，防止 npm/pnpm 无限挂起
func pluginEnv() []string {
	registry := GetConfig().GetNpmRegistry()
	return append(os.Environ(),
		"NPM_CONFIG_FETCH_TIMEOUT=30000",
		"NPM_CONFIG_NETWORK_TIMEOUT=30000",
		"NPM_CONFIG_FETCH_RETRIES=2",
		"NPM_CONFIG_FETCH_RETRY_MINTIMEOUT=2000",
		"NPM_CONFIG_FETCH_RETRY_MAXTIMEOUT=10000",
		"PNPM_CONFIG_FETCH_TIMEOUT=30000",
		"PNPM_CONFIG_NETWORK_TIMEOUT=30000",
		"PNPM_CONFIG_FETCH_RETRIES=2",
		"PNPM_CONFIG_REGISTRY="+registry,
		"NPM_CONFIG_REGISTRY="+registry,
		"pnpm_config_registry="+registry,
		"npm_config_registry="+registry,
		"PNPM_CONFIG_MINIMUM_RELEASE_AGE=0",
		"pnpm_config_minimum_release_age=0",
	)
}

type pluginVerb string

const (
	pluginAdd     pluginVerb = "add"
	pluginRemove  pluginVerb = "remove"
	pluginUpdate  pluginVerb = "update"
	pluginInstall pluginVerb = "install"
)

var pluginVerbAliases = map[string]pluginVerb{
	"add":     pluginAdd,
	"install": pluginInstall, "i": pluginInstall,
	"remove": pluginRemove, "rm": pluginRemove, "uninstall": pluginRemove, "un": pluginRemove,
	"update": pluginUpdate, "up": pluginUpdate, "upgrade": pluginUpdate,
}

var pluginNeedSpecs = map[pluginVerb]bool{
	pluginAdd: true, pluginRemove: true, pluginUpdate: true,
}

var (
	npmSpecRe       = regexp.MustCompile(`^(@[a-z0-9-~][\w.-]*\/)?[a-z0-9-~][\w.-]*(@[0-9A-Za-z.*+~^<>=,\- ]+)?$`)
	gitURLRe        = regexp.MustCompile(`^(git\+)?(https?:\/\/|ssh:\/\/)[^\s;|` + "`" + `$()]+$`)
	gitShorthandRe  = regexp.MustCompile(`^github:[a-zA-Z0-9_.-]+\/[a-zA-Z0-9_.-]+(?:#[^\s;|` + "`" + `$()]+)?$`)
	localSpecRe     = regexp.MustCompile(`^(file:|\/).+$`)
	specForbiddenRe = regexp.MustCompile(`[;|` + "`" + `$()\r\n]`)
)

func splitCommandLine(input string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	escaped := false

	for i := 0; i < len(input); i++ {
		c := input[i]
		if escaped {
			cur.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if inQuote {
			if c == quoteChar {
				inQuote = false
				quoteChar = 0
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = true
			quoteChar = c
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(c)
	}
	if inQuote {
		return nil, fmt.Errorf("引号未闭合")
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

func validatePluginSpec(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return fmt.Errorf("包名为空")
	}
	if specForbiddenRe.MatchString(spec) {
		return fmt.Errorf("包含不允许的字符")
	}
	if npmSpecRe.MatchString(spec) || gitURLRe.MatchString(spec) || gitShorthandRe.MatchString(spec) || localSpecRe.MatchString(spec) {
		return nil
	}
	if strings.HasPrefix(spec, ".") {
		return fmt.Errorf("不支持相对路径，请输入标准 npm 包名或 Git 仓库地址")
	}
	return fmt.Errorf("无法识别的包名/地址格式")
}

type pluginCommand struct {
	Verb    pluginVerb
	Profile string
	Specs   []string
}

func parsePluginCommand(input string) (*pluginCommand, error) {
	fields, err := splitCommandLine(strings.TrimSpace(input))
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("请输入插件命令或包名")
	}

	// 兼容：直接输入包名时自动补全为标准添加命令
	if fields[0] != "dsh" {
		fields = append([]string{"dsh", "plugin", "--profile", "web", "add"}, fields...)
	}

	if len(fields) < 2 || fields[0] != "dsh" || fields[1] != "plugin" {
		return nil, fmt.Errorf("请输入标准 dsh 命令，例如: dsh plugin --profile web add 包名")
	}

	profile := "web"
	rest := make([]string, 0, len(fields)-2)
	for i := 2; i < len(fields); i++ {
		tok := fields[i]
		if tok == "--profile" {
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("--profile 缺少参数")
			}
			name := fields[i+1]
			if name != "web" {
				return nil, fmt.Errorf("当前仅支持 web 参数 (--profile web)")
			}
			profile = "web"
			i++
			continue
		}
		if strings.HasPrefix(tok, "--") {
			return nil, fmt.Errorf("不支持的参数: %s", tok)
		}
		rest = append(rest, tok)
	}
	if len(rest) == 0 {
		return nil, fmt.Errorf("缺少操作动词（支持 add / remove / update / list / why / install）")
	}

	verb, ok := pluginVerbAliases[rest[0]]
	if !ok {
		return nil, fmt.Errorf("未知操作 %q（支持 add / remove / update / list / why / install）", rest[0])
	}
	cmd := &pluginCommand{Verb: verb, Profile: profile, Specs: rest[1:]}

	if pluginNeedSpecs[cmd.Verb] && len(cmd.Specs) == 0 {
		return nil, fmt.Errorf("%s 操作需要一个或多个包名", cmd.Verb)
	}
	if cmd.Verb == pluginInstall && len(cmd.Specs) > 0 {
		return nil, fmt.Errorf("install 操作不接受包名参数")
	}
	if len(cmd.Specs) == 0 {
		cmd.Specs = nil
	}
	for _, s := range cmd.Specs {
		if err := validatePluginSpec(s); err != nil {
			return nil, fmt.Errorf("参数 %q: %s", s, err)
		}
	}
	return cmd, nil
}

func profileHasWorkspace() bool {
	_, err := os.Stat(filepath.Join(pluginProfileDir(), "pnpm-workspace.yaml"))
	return err == nil
}

func (c *pluginCommand) dshArgs() []string {
	args := []string{"plugin", "--profile", c.Profile, string(c.Verb)}
	if c.Verb != pluginInstall && profileHasWorkspace() {
		args = append(args, "-w")
	}
	args = append(args, "--config.minimumReleaseAge=0")
	args = append(args, c.Specs...)
	return args
}

func (c *pluginCommand) display() string {
	return "dsh plugin --profile " + c.Profile + " " + string(c.Verb) + " " + strings.Join(c.Specs, " ")
}

func pluginProfileDir() string {
	return filepath.Join(globalDshHome, "profiles", "web")
}

// pluginItem 插件列表展示模型
type pluginItem struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Spec        string   `json:"spec,omitempty"`
	State       string   `json:"state"` // "live", "disabled", "inert"
	EntryIDs    []string `json:"entryIds,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	IsProtected bool     `json:"isProtected"`
	HasBundle   bool     `json:"hasBundle"`
}

type pluginListPayload struct {
	Profile string       `json:"profile"`
	Plugins []pluginItem `json:"plugins"`
	Bundles []string     `json:"bundles"`
}

// checkDuplicatePlugin 检查插件是否已经安装
func checkDuplicatePlugin(spec string) error {
	norm := normalizePluginKey(spec)
	m, err := readProfileManifestFile(pluginProfileDir())
	if err != nil {
		return nil
	}
	if currentSpec, exists := m.Dependencies[norm]; exists {
		return fmt.Errorf("插件「%s」已安装 (当前版本: %s)", norm, currentSpec)
	}
	return nil
}

type rawPackageMeta struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	License     string   `json:"license"`
	Keywords    []string `json:"keywords"`
	Author      any      `json:"author"`
	Dsh         *struct {
		Bundle *struct {
			Patch string `json:"patch"`
		} `json:"bundle"`
	} `json:"dsh"`
}

func installedPluginMetadata(name string) (meta rawPackageMeta, found bool) {
	candidates := []string{
		filepath.Join(pluginProfileDir(), "node_modules", name, "package.json"),
		filepath.Join(globalDshHome, "profiles", "node_modules", name, "package.json"),
		filepath.Join(runtimeDir, "node_modules", name, "package.json"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if json.Unmarshal(data, &meta) == nil && meta.Name != "" {
			return meta, true
		}
	}
	return meta, false
}

func parseAuthorString(author any) string {
	if author == nil {
		return ""
	}
	if s, ok := author.(string); ok {
		return s
	}
	if m, ok := author.(map[string]any); ok {
		if name, ok := m["name"].(string); ok {
			return name
		}
	}
	return ""
}

// profileSnapshot 配置文件快照
type profileSnapshot struct {
	pkgJSON  []byte
	pnpmLock []byte
}

func takeProfileSnapshot(dir string) *profileSnapshot {
	snap := &profileSnapshot{}
	snap.pkgJSON, _ = os.ReadFile(filepath.Join(dir, "package.json"))
	snap.pnpmLock, _ = os.ReadFile(filepath.Join(dir, "pnpm-lock.yaml"))
	return snap
}

func restoreProfileSnapshot(dir string, snap *profileSnapshot) {
	if snap == nil {
		return
	}
	if len(snap.pkgJSON) > 0 {
		_ = os.WriteFile(filepath.Join(dir, "package.json"), snap.pkgJSON, 0644)
	}
	if len(snap.pnpmLock) > 0 {
		_ = os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), snap.pnpmLock, 0644)
	}
}

func handleListPlugins(c *gin.Context) {
	dir := pluginProfileDir()
	m, err := readProfileManifestFile(dir)
	if err != nil {
		if os.IsNotExist(err) {
			OK(c, pluginListPayload{Profile: "web", Plugins: []pluginItem{}, Bundles: []string{}})
			return
		}
		Fail(c, http.StatusInternalServerError, "读取插件列表失败: "+err.Error())
		return
	}

	bundleSet := make(map[string]bool, len(m.Dsh.Profile.Bundles))
	for _, b := range m.Dsh.Profile.Bundles {
		bundleSet[b] = true
	}

	namesSet := make(map[string]bool, len(m.Dependencies)+len(m.Dsh.Profile.Bundles))
	for name := range m.Dependencies {
		namesSet[name] = true
	}
	for _, b := range m.Dsh.Profile.Bundles {
		namesSet[b] = true
	}

	names := make([]string, 0, len(namesSet))
	for name := range namesSet {
		names = append(names, name)
	}
	sort.Strings(names)

	plugins := make([]pluginItem, 0, len(names))
	for _, name := range names {
		meta, _ := installedPluginMetadata(name)
		hasBundle := meta.Dsh != nil && meta.Dsh.Bundle != nil && meta.Dsh.Bundle.Patch != ""
		isProtected := IsProtectedPlugin(name)

		stateVal := "inert"
		if hasBundle {
			if bundleSet[name] {
				stateVal = "live"
			} else {
				stateVal = "disabled"
			}
		}

		plugins = append(plugins, pluginItem{
			Name:        name,
			Version:     meta.Version,
			Spec:        m.Dependencies[name],
			State:       stateVal,
			EntryIDs:    []string{name},
			Description: meta.Description,
			Author:      parseAuthorString(meta.Author),
			Homepage:    meta.Homepage,
			License:     meta.License,
			Keywords:    meta.Keywords,
			IsProtected: isProtected,
			HasBundle:   hasBundle,
		})
	}

	OK(c, pluginListPayload{
		Profile: "web",
		Plugins: plugins,
		Bundles: m.Dsh.Profile.Bundles,
	})
}

func handlePluginStatus(c *gin.Context) {
	OK(c, pluginStatusPayload())
}

type pluginOpState struct {
	Running bool   `json:"running"`
	OK      *bool  `json:"ok,omitempty"`
	Message string `json:"message,omitempty"`
}

var (
	pluginStateMu sync.Mutex
	pluginOp      pluginOpState
	pluginSubs    = make(map[chan struct{}]struct{})
	pluginSubsMu  sync.Mutex
)

func setPluginRunning() error {
	pluginStateMu.Lock()
	defer pluginStateMu.Unlock()
	if pluginOp.Running {
		return fmt.Errorf("插件操作正在进行中，请稍候")
	}
	if state.Status() == StatusBuilding {
		return fmt.Errorf("正在部署更新中，请稍候再试")
	}
	if state.Status() == StatusStarting {
		return fmt.Errorf("服务正在启动中，请稍候再试")
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "node_modules")); err != nil {
		return fmt.Errorf("运行环境未就绪或依赖文件缺失")
	}
	if err := installPnpm(); err != nil {
		return fmt.Errorf("初始化 pnpm 运行环境失败: %w", err)
	}
	pluginOp = pluginOpState{Running: true}
	notifyPlugin()
	return nil
}

func setPluginDone(ok bool, msg string) {
	pluginStateMu.Lock()
	pluginOp = pluginOpState{Running: false, OK: &ok, Message: msg}
	pluginStateMu.Unlock()
	notifyPlugin()
}

func pluginStatusPayload() pluginOpState {
	pluginStateMu.Lock()
	defer pluginStateMu.Unlock()
	return pluginOp
}

func SubscribePlugin(buf int) (<-chan struct{}, func()) {
	pluginSubsMu.Lock()
	defer pluginSubsMu.Unlock()
	ch := make(chan struct{}, buf)
	pluginSubs[ch] = struct{}{}
	return ch, func() {
		pluginSubsMu.Lock()
		delete(pluginSubs, ch)
		pluginSubsMu.Unlock()
	}
}

func notifyPlugin() {
	pluginSubsMu.Lock()
	subs := make([]chan struct{}, 0, len(pluginSubs))
	for ch := range pluginSubs {
		subs = append(subs, ch)
	}
	pluginSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type tailWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTailWriter(max int) *tailWriter {
	return &tailWriter{max: max}
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= t.max {
		t.buf = append([]byte(nil), p[len(p)-t.max:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func pluginFailMessage(err error, tail string) string {
	msg := err.Error()
	if tail = strings.TrimSpace(tail); tail != "" {
		msg += "\n" + tail
	}
	if strings.Contains(tail, "ERR_PNPM_IGNORED_BUILDS") ||
		strings.Contains(tail, "approve-builds") ||
		strings.Contains(tail, "allowBuilds") {
		msg += "\n构建脚本被 pnpm 拦截，已自动配置放行并重试。"
	}
	return msg
}

var (
	activePluginCmdMu    sync.Mutex
	activePluginCmd      *exec.Cmd
	activePluginCanceled bool
	activePluginTimedOut bool
)

// cancelActivePlugin 终止当前正在运行的插件进程树
func cancelActivePlugin() bool {
	activePluginCmdMu.Lock()
	defer activePluginCmdMu.Unlock()
	if activePluginCmd == nil || activePluginCmd.Process == nil {
		return false
	}
	activePluginCanceled = true
	pid := activePluginCmd.Process.Pid
	LogWarning("[插件] 收到取消请求，正在终止插件操作进程组 (PID: %d)...", pid)
	go killProcessTree(pid)
	return true
}

func handlePluginCancel(c *gin.Context) {
	if !cancelActivePlugin() {
		Fail(c, http.StatusBadRequest, "当前没有正在执行的插件操作")
		return
	}
	OKMsg(c, "已发送取消指令", nil)
}

func runPluginSubprocess(cmdArgs []string, timeout time.Duration) error {
	tail := newTailWriter(800)
	outWriter := NewLogWriter()
	errWriter := NewLogWriter()
	defer outWriter.Flush()
	defer errWriter.Flush()

	bin, args := dshCliCmd(cmdArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = runtimeDir
	cmd.Env = pluginEnv()
	setProcessGroup(cmd)
	cmd.Stdout = io.MultiWriter(outWriter, tail)
	cmd.Stderr = io.MultiWriter(errWriter, tail)

	if err := cmd.Start(); err != nil {
		return err
	}

	activePluginCmdMu.Lock()
	activePluginCmd = cmd
	activePluginCanceled = false
	activePluginTimedOut = false
	activePluginCmdMu.Unlock()

	defer func() {
		activePluginCmdMu.Lock()
		activePluginCmd = nil
		activePluginCmdMu.Unlock()
	}()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var timer *time.Timer
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case <-timeoutCh:
		activePluginCmdMu.Lock()
		activePluginTimedOut = true
		activePluginCmdMu.Unlock()
		if cmd.Process != nil {
			pid := cmd.Process.Pid
			LogWarning("[插件] 操作执行超时 (%v)，正在强制终止进程组 (PID: %d)...", timeout, pid)
			killProcessTree(pid)
		}
		_ = <-done
		return fmt.Errorf("插件操作超时（超过 %v），已自动终止", timeout)
	case err := <-done:
		if err != nil {
			activePluginCmdMu.Lock()
			canceled := activePluginCanceled
			timedOut := activePluginTimedOut
			activePluginCmdMu.Unlock()
			if canceled {
				return fmt.Errorf("操作已被用户手动取消")
			}
			if timedOut {
				return fmt.Errorf("插件操作超时（超过 %v），已自动终止", timeout)
			}
			return fmt.Errorf("%s", pluginFailMessage(err, tail.String()))
		}
		return nil
	}
}

func pluginOpTimeout(verb pluginVerb) time.Duration {
	if verb == pluginRemove {
		return pluginRemoveTimeout
	}
	return pluginInstallTimeout
}

func runPluginOpWithRecovery(cmd *pluginCommand, doneMsg string) (string, error) {
	timeout := pluginOpTimeout(cmd.Verb)
	args := cmd.dshArgs()

	runErr := runPluginSubprocess(args, timeout)
	if runErr == nil {
		return doneMsg, nil
	}

	// 构建脚本拦截自愈：放行依赖构建脚本并重试一次
	pkgs := parseBlockedPackages(runErr.Error())
	if len(pkgs) > 0 {
		if err := approveBuilds(pluginProfileDir(), pkgs); err == nil {
			LogWarning("[插件] 构建脚本被拦截 [%s]，已放行并重试", strings.Join(pkgs, ", "))
			if runErr = runPluginSubprocess(args, timeout); runErr == nil {
				return doneMsg + "（已放行构建脚本: " + strings.Join(pkgs, ", ") + "）", nil
			}
		}
	}

	return "", fmt.Errorf("%s", FormatPnpmFailureMessage(runErr.Error()))
}

func launchPluginOp(cmd *pluginCommand, doneMsg string) {
	LogInfo("[插件] 开始执行操作: verb=%s, specs=%v, profile=%s", cmd.Verb, cmd.Specs, cmd.Profile)
	go func() {
		profileDir := pluginProfileDir()

		// 卸载前先反激活 Bundle
		if cmd.Verb == pluginRemove {
			for _, spec := range cmd.Specs {
				_ = selectBundle(profileDir, normalizePluginKey(spec), false)
			}
		}

		// 更新前记录旧版本号
		beforeVersions := make(map[string]string)
		if cmd.Verb == pluginUpdate {
			for _, spec := range cmd.Specs {
				name := normalizePluginKey(spec)
				if meta, ok := installedPluginMetadata(name); ok {
					beforeVersions[name] = meta.Version
				}
			}
		}

		// 安装操作前暂存事务快照
		var snap *profileSnapshot
		if cmd.Verb == pluginAdd || cmd.Verb == pluginInstall {
			snap = takeProfileSnapshot(profileDir)
		}

		msg, runErr := runPluginOpWithRecovery(cmd, doneMsg)
		if runErr != nil {
			LogWarning("[插件] 插件操作执行失败: %s", runErr)
			// 失败回滚快照
			if snap != nil {
				restoreProfileSnapshot(profileDir, snap)
			}
			setPluginDone(false, runErr.Error())
			return
		}

		// 激活插件 Bundle
		if cmd.Verb == pluginAdd {
			for _, s := range cmd.Specs {
				name := normalizePluginKey(s)
				if meta, found := installedPluginMetadata(name); found {
					if meta.Dsh != nil && meta.Dsh.Bundle != nil && meta.Dsh.Bundle.Patch != "" {
						_ = selectBundle(profileDir, name, true)
					}
				}
			}
		}

		// 移除 Bundle 与构建放行项
		if cmd.Verb == pluginRemove {
			var keys []string
			for _, s := range cmd.Specs {
				name := normalizePluginKey(s)
				keys = append(keys, name)
				_ = selectBundle(profileDir, name, false)
			}
			_ = removeAllowBuilds(profileDir, keys)
		}

		// 版本比对反馈
		if cmd.Verb == pluginUpdate && len(cmd.Specs) > 0 {
			var updatedDetails []string
			hasAnyUpgrade := false
			for _, spec := range cmd.Specs {
				name := normalizePluginKey(spec)
				oldVer := beforeVersions[name]
				newMeta, found := installedPluginMetadata(name)
				if found && oldVer != "" {
					if CompareSemver(newMeta.Version, oldVer) > 0 {
						hasAnyUpgrade = true
						updatedDetails = append(updatedDetails, fmt.Sprintf("%s: v%s -> v%s", name, oldVer, newMeta.Version))
					} else if newMeta.Version == oldVer {
						updatedDetails = append(updatedDetails, fmt.Sprintf("%s: 当前已是最新版本 (v%s)", name, newMeta.Version))
					} else {
						hasAnyUpgrade = true
						updatedDetails = append(updatedDetails, fmt.Sprintf("%s: v%s", name, newMeta.Version))
					}
				}
			}
			if !hasAnyUpgrade && len(updatedDetails) > 0 {
				msg = strings.Join(updatedDetails, "；") + "，远端无新发布版本"
			} else if len(updatedDetails) > 0 {
				msg = "更新完成（" + strings.Join(updatedDetails, "；") + "），重启服务后生效"
			}
		}

		LogInfo("[插件] 插件操作执行完成: %s", msg)
		setPluginDone(true, msg)
	}()
}

func validatePluginExecution(cmd *pluginCommand) error {
	switch cmd.Verb {
	case pluginAdd:
		for _, spec := range cmd.Specs {
			if IsProtectedPlugin(spec) {
				return fmt.Errorf("系统核心模块「%s」受到保护，禁止覆盖安装", spec)
			}
			if err := checkDuplicatePlugin(spec); err != nil {
				return err
			}
		}
	case pluginRemove:
		for _, spec := range cmd.Specs {
			if IsProtectedPlugin(spec) {
				return fmt.Errorf("系统核心模块「%s」受到保护，只读禁止卸载", spec)
			}
		}
	case pluginUpdate:
		for _, spec := range cmd.Specs {
			if IsProtectedPlugin(spec) {
				return fmt.Errorf("系统核心模块「%s」受到保护，只读禁止单独更新", spec)
			}
		}
	}
	return nil
}

func pluginPreviewError(cmd *pluginCommand) string {
	if cmd.Verb != pluginAdd {
		return "仅支持添加插件指令 (add)"
	}
	if err := validatePluginExecution(cmd); err != nil {
		return err.Error()
	}
	return ""
}

func handlePluginPreview(c *gin.Context) {
	var req struct {
		Command string `json:"command"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	cmd, err := parsePluginCommand(req.Command)
	if err != nil {
		OK(c, gin.H{"valid": false, "ok": false, "reason": err.Error()})
		return
	}
	if reason := pluginPreviewError(cmd); reason != "" {
		OK(c, gin.H{"valid": false, "ok": false, "reason": reason})
		return
	}
	OK(c, gin.H{
		"valid":   true,
		"ok":      true,
		"verb":    cmd.Verb,
		"profile": cmd.Profile,
		"specs":   cmd.Specs,
		"command": cmd.display(),
	})
}

func handlePluginRun(c *gin.Context) {
	var req struct {
		Command string `json:"command"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	cmd, err := parsePluginCommand(req.Command)
	if err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePluginExecution(cmd); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := setPluginRunning(); err != nil {
		Fail(c, http.StatusConflict, err.Error())
		return
	}
	LogInfo("[插件] 执行插件指令: %s", cmd.display())

	doneMsg := "操作完成"
	var startMsg string
	switch cmd.Verb {
	case pluginAdd:
		doneMsg = "安装完成，重启服务后生效"
		startMsg = "已开始执行插件安装"
	case pluginRemove:
		doneMsg = "卸载完成，重启服务后生效"
		startMsg = fmt.Sprintf("已开始卸载插件「%s」", strings.Join(cmd.Specs, " "))
	case pluginUpdate:
		doneMsg = "更新完成，重启服务后生效"
		startMsg = fmt.Sprintf("已开始更新插件「%s」", strings.Join(cmd.Specs, " "))
	case pluginInstall:
		doneMsg = "安装完成，重启服务后生效"
		startMsg = "已开始执行插件安装"
	default:
		startMsg = "已开始执行插件指令"
	}
	launchPluginOp(cmd, doneMsg)
	OKMsg(c, startMsg, gin.H{"command": cmd.display()})
}

// handlePluginToggle 对齐官方 selectBundle 机制，仅通过 package.json 的 dsh.profile.bundles 启停
func handlePluginToggle(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Name == "" {
		Fail(c, http.StatusBadRequest, "缺少插件名")
		return
	}

	if IsProtectedPlugin(req.Name) {
		Fail(c, http.StatusForbidden, fmt.Sprintf("核心基础设施插件「%s」受到保护，禁止更改启停状态", req.Name))
		return
	}

	dir := pluginProfileDir()
	if err := selectBundle(dir, req.Name, req.Enabled); err != nil {
		LogWarning("[插件] 切换插件状态失败 [%s]: %s", req.Name, err)
		Fail(c, http.StatusInternalServerError, "切换插件状态失败: "+err.Error())
		return
	}

	action := "已启用"
	if !req.Enabled {
		action = "已禁用"
	}
	msg := fmt.Sprintf("%s插件「%s」", action, req.Name)
	OKMsg(c, msg, gin.H{"name": req.Name, "enabled": req.Enabled})
}
