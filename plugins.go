package main

import (
	"bytes"
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
	pluginRemoveTimeout  = 60 * time.Second
	pluginInstallTimeout = 180 * time.Second
	pluginSyncTimeout    = 30 * time.Second
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
	pluginList    pluginVerb = "list"
	pluginWhy     pluginVerb = "why"
	pluginInstall pluginVerb = "install"
)

var pluginVerbAliases = map[string]pluginVerb{
	"add":     pluginAdd,
	"install": pluginInstall, "i": pluginInstall,
	"remove": pluginRemove, "rm": pluginRemove, "uninstall": pluginRemove, "un": pluginRemove,
	"update": pluginUpdate, "up": pluginUpdate, "upgrade": pluginUpdate,
	"list": pluginList, "ls": pluginList,
	"why": pluginWhy,
}

var pluginNeedSpecs = map[pluginVerb]bool{
	pluginAdd: true, pluginRemove: true, pluginUpdate: true, pluginWhy: true,
}

var (
	npmSpecRe       = regexp.MustCompile(`^(@[a-z0-9-~][\w.-]*\/)?[a-z0-9-~][\w.-]*(@[0-9A-Za-z.*+~^<>=,\- ]+)?$`)
	gitURLRe        = regexp.MustCompile(`^(git\+)?(https?:\/\/|ssh:\/\/)[^\s;|` + "`" + `$()]+$`)
	gitShorthandRe  = regexp.MustCompile(`^github:[a-zA-Z0-9_.-]+\/[a-zA-Z0-9_.-]+(?:#[^\s;|` + "`" + `$()]+)?$`)
	localSpecRe     = regexp.MustCompile(`^(file:|\/).+$`)
	profileNameRe   = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
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
	Verb     pluginVerb
	Profile  string
	Specs    []string
	AllowKey string
}

func parsePluginCommand(input string) (*pluginCommand, error) {
	fields, err := splitCommandLine(strings.TrimSpace(input))
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("请输入插件命令")
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
			if !profileNameRe.MatchString(name) {
				return nil, fmt.Errorf("非法的 profile 名称: %s", name)
			}
			profile = name
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
	if c.Verb != pluginList && c.Verb != pluginWhy {
		if c.Verb != pluginInstall && profileHasWorkspace() {
			args = append(args, "-w")
		}
		args = append(args, "--config.minimumReleaseAge=0")
	}
	args = append(args, c.Specs...)
	return args
}

func (c *pluginCommand) display() string {
	return "dsh plugin --profile " + c.Profile + " " + string(c.Verb) + " " + strings.Join(c.Specs, " ")
}

func pluginProfileDir() string {
	return filepath.Join(globalDshHome, "profiles", "web")
}

// pluginItem 前端呈现的元数据插件模型
type pluginItem struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Spec        string   `json:"spec,omitempty"`
	State       string   `json:"state"` // "live", "disabled", "inert"
	Layer       bool     `json:"layer"` // 兼容字段: State == "live"
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

type profileManifest struct {
	Name         string            `json:"name"`
	Private      bool              `json:"private"`
	Version      string            `json:"version,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
	Dsh          *struct {
		Profile *struct {
			Bundles  []string `json:"bundles"`
			Disabled []string `json:"disabled"` // 兼容旧版 disabled 字段
		} `json:"profile"`
	} `json:"dsh,omitempty"`
}

func profileManifestPath() string {
	return filepath.Join(pluginProfileDir(), "package.json")
}

// checkDuplicatePlugin 检查插件是否已经安装
func checkDuplicatePlugin(spec string) error {
	norm := normalizePluginKey(spec)
	deps, _, _, err := readProfileManifest()
	if err != nil {
		return nil
	}
	if currentSpec, exists := deps[norm]; exists {
		return fmt.Errorf("插件「%s」已安装 (当前版本: %s)", norm, currentSpec)
	}
	return nil
}

func readProfileManifest() (deps map[string]string, bundles []string, legacyDisabled []string, err error) {
	data, err := os.ReadFile(profileManifestPath())
	if err != nil {
		return nil, nil, nil, err
	}
	var m profileManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, nil, err
	}
	if m.Dependencies == nil {
		m.Dependencies = map[string]string{}
	}
	if m.Dsh != nil && m.Dsh.Profile != nil {
		bundles = m.Dsh.Profile.Bundles
		legacyDisabled = m.Dsh.Profile.Disabled
	}
	return m.Dependencies, bundles, legacyDisabled, nil
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
		Client any `json:"client"`
	} `json:"dsh"`
}

func installedPluginMetadata(name string) (meta rawPackageMeta, found bool) {
	candidates := []string{
		filepath.Join(pluginProfileDir(), "node_modules", name, "package.json"),
		filepath.Join(globalDshHome, "profiles", "node_modules", name, "package.json"),
		filepath.Join(srcDir, "node_modules", name, "package.json"),
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

func handleListPlugins(c *gin.Context) {
	deps, bundles, legacyDisabled, err := readProfileManifest()
	if err != nil {
		if os.IsNotExist(err) {
			OK(c, pluginListPayload{Profile: "web", Plugins: []pluginItem{}, Bundles: []string{}})
			return
		}
		Fail(c, http.StatusInternalServerError, "读取插件列表失败: "+err.Error())
		return
	}

	bundleSet := make(map[string]bool, len(bundles))
	for _, b := range bundles {
		bundleSet[b] = true
	}

	// 官方 Cordis User Patch 中的禁用状态映射
	disabledMap, _ := ReadDisabledEntryMap("web")
	if disabledMap == nil {
		disabledMap = make(map[string]bool)
	}

	// 自动迁移：检测旧版 package.json 中的 disabled 状态并同步写入官方 cordis.patch.yml
	if len(legacyDisabled) > 0 {
		for _, disName := range legacyDisabled {
			disName = strings.TrimSpace(disName)
			if disName == "" {
				continue
			}
			isAlreadyDisabled := false
			entryIDs := ExtractPluginEntryIDs("web", disName)
			for _, eid := range entryIDs {
				if disabledMap[eid] {
					isAlreadyDisabled = true
					break
				}
			}
			if !isAlreadyDisabled {
				_ = SetPluginDisabled("web", disName, true)
				for _, eid := range entryIDs {
					disabledMap[eid] = true
				}
				LogInfo("[历史配置迁移] 检测到旧版 package.json 中的 disabled 状态，已自动无缝迁移至官方 cordis.patch.yml: %s", disName)
			}
		}
	}

	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)

	plugins := make([]pluginItem, 0, len(names))
	for _, name := range names {
		meta, _ := installedPluginMetadata(name)
		entryIDs := ExtractPluginEntryIDs("web", name)
		hasBundle := meta.Dsh != nil && meta.Dsh.Bundle != nil && meta.Dsh.Bundle.Patch != ""
		isProtected := IsProtectedPlugin(name)

		// 判定插件当前状态
		stateVal := "live"
		isDisabled := false
		for _, eid := range entryIDs {
			if disabledMap[eid] {
				isDisabled = true
				break
			}
		}
		if isDisabled {
			stateVal = "disabled"
		} else if !hasBundle && !bundleSet[name] {
			// 未声明 dsh.bundle，作为普通依赖存在
			stateVal = "inert"
		}

		plugins = append(plugins, pluginItem{
			Name:        name,
			Version:     meta.Version,
			Spec:        deps[name],
			State:       stateVal,
			Layer:       (stateVal == "live"),
			EntryIDs:    entryIDs,
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
		Bundles: bundles,
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
		return fmt.Errorf("正在构建中，请稍候再试")
	}
	if state.Status() == StatusStarting {
		return fmt.Errorf("服务正在启动中，请稍候再试")
	}
	if _, err := os.Stat(filepath.Join(srcDir, "node_modules")); err != nil {
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
	LogWarning("[插件管理] 收到取消请求，正在终止插件操作进程组 (PID: %d)...", pid)
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
	outWriter := NewLogWriterInfo()
	errWriter := NewLogWriterWarn()
	defer outWriter.Flush()
	defer errWriter.Flush()

	bin, args := dshCliCmd(cmdArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = srcDir
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
			LogWarning("[插件管理] 操作执行超时 (%v)，正在强制终止进程组 (PID: %d)...", timeout, pid)
			killProcessTree(pid)
		}
		_ = <-done
		return fmt.Errorf("插件操作超时（超过 %v），网络请求或依赖解析未能按时完成，已自动终止", timeout)
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

func runPluginSync(cmdArgs []string, timeout time.Duration) (string, error) {
	outWriter := NewLogWriterInfo()
	errWriter := NewLogWriterWarn()
	defer outWriter.Flush()
	defer errWriter.Flush()

	bin, args := dshCliCmd(cmdArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = srcDir
	cmd.Env = pluginEnv()
	setProcessGroup(cmd)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(outWriter, &buf)
	cmd.Stderr = io.MultiWriter(errWriter, &buf)

	if err := cmd.Start(); err != nil {
		return "", err
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
			LogWarning("[插件管理] 同步操作执行超时 (%v)，正在强制终止进程组 (PID: %d)...", timeout, pid)
			killProcessTree(pid)
		}
		_ = <-done
		return buf.String(), fmt.Errorf("插件操作超时（超过 %v），已自动终止", timeout)
	case err := <-done:
		if err != nil {
			activePluginCmdMu.Lock()
			canceled := activePluginCanceled
			timedOut := activePluginTimedOut
			activePluginCmdMu.Unlock()
			if canceled {
				return "", fmt.Errorf("操作已被用户手动取消")
			}
			if timedOut {
				return "", fmt.Errorf("插件操作超时（超过 %v），已自动终止", timeout)
			}
			return buf.String(), fmt.Errorf("%s", pluginFailMessage(err, buf.String()))
		}
		return buf.String(), nil
	}
}

func pluginAllowKey(cmd *pluginCommand) string {
	if cmd.AllowKey != "" {
		return cmd.AllowKey
	}
	if len(cmd.Specs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(cmd.Specs))
	for _, s := range cmd.Specs {
		keys = append(keys, normalizePluginKey(s))
	}
	return strings.Join(keys, " ")
}

func pluginOpTimeout(verb pluginVerb) time.Duration {
	switch verb {
	case pluginRemove:
		return pluginRemoveTimeout
	case pluginAdd, pluginUpdate, pluginInstall:
		return pluginInstallTimeout
	default:
		return pluginSyncTimeout
	}
}

func runPluginOpWithRecovery(cmd *pluginCommand, doneMsg string) (string, error) {
	timeout := pluginOpTimeout(cmd.Verb)
	args := cmd.dshArgs()

	if cmd.Verb == pluginList || cmd.Verb == pluginWhy {
		out, runErr := runPluginSync(args, timeout)
		if runErr != nil {
			return "", fmt.Errorf("%s", FormatPnpmFailureMessage(runErr.Error()))
		}
		return strings.TrimSpace(out), nil
	}

	runErr := runPluginSubprocess(args, timeout)
	if runErr == nil {
		return doneMsg, nil
	}

	if cmd.Verb != pluginAdd && cmd.Verb != pluginUpdate && cmd.Verb != pluginInstall && cmd.Verb != pluginRemove {
		return "", fmt.Errorf("%s", FormatPnpmFailureMessage(runErr.Error()))
	}

	failure := ClassifyPnpmFailure(runErr.Error())

	// 依赖结构差异自愈
	if failure.Code == PnpmFailureHoistPatternDiff {
		LogWarning("[自动自愈] 依赖结构存在跨版本差异，正在执行重建 (pnpm install --no-frozen-lockfile)...")
		_ = runPluginSubprocess([]string{"plugin", "--profile", cmd.Profile, "install", "--no-frozen-lockfile"}, timeout)
		if runErr = runPluginSubprocess(args, timeout); runErr == nil {
			return doneMsg + "（已自动重建依赖环境）", nil
		}
		failure = ClassifyPnpmFailure(runErr.Error())
	}

	// 存储位置异常自愈
	if failure.Code == PnpmFailureUnexpectedStore {
		_ = os.RemoveAll(filepath.Join(pluginProfileDir(), "node_modules"))
		LogWarning("[自动自愈] 存储位置变更，已自动清理本地缓存并重试: %s", cmd.display())
		if runErr = runPluginSubprocess(args, timeout); runErr == nil {
			return doneMsg, nil
		}
		failure = ClassifyPnpmFailure(runErr.Error())
	}


	// 大包下载超时自愈
	if failure.Code == PnpmFailureFetchTimeout {
		LogWarning("[自动自愈] 大包下载超时，正在以 10 分钟超时延长重试...")
		retryArgs := append([]string{}, args...)
		retryArgs = append(retryArgs, "--config.fetchTimeout=600000")
		if runErr = runPluginSubprocess(retryArgs, timeout+10*time.Minute); runErr == nil {
			return doneMsg + "（已自动延长超时完成下载）", nil
		}
		failure = ClassifyPnpmFailure(runErr.Error())
	}

	// 网络波动重试自愈
	if failure.Code == PnpmFailureTransientNetwork {
		LogWarning("[自动自愈] 检测到网络瞬时波动，正在自动重试 1 次...")
		if runErr = runPluginSubprocess(args, timeout); runErr == nil {
			return doneMsg, nil
		}
		failure = ClassifyPnpmFailure(runErr.Error())
	}

	// 构建脚本拦截自愈
	pkgs := parseBlockedPackages(runErr.Error())
	if len(pkgs) > 0 {
		if err := ensureAllowBuildsFor(cmd.Profile, pluginAllowKey(cmd), pkgs); err == nil {
			LogWarning("[自动自愈] 构建脚本被拦截 [%s]，已自动配置放行并重新执行", strings.Join(pkgs, ", "))
			if runErr = runPluginSubprocess(args, timeout); runErr == nil {
				return doneMsg + "（已自动放行构建脚本: " + strings.Join(pkgs, ", ") + "）", nil
			}
		}
	}

	// 所有重试均未成功，提炼友好中文报错
	return "", fmt.Errorf("%s", FormatPnpmFailureMessage(runErr.Error()))
}

func launchPluginOp(cmd *pluginCommand, doneMsg string) {
	LogInfo("开始执行插件管理操作: verb=%s, specs=%v, profile=%s", cmd.Verb, cmd.Specs, cmd.Profile)
	go func() {
		// 更新前记录旧版本号（用于后续陈旧性比对）
		beforeVersions := make(map[string]string)
		if cmd.Verb == pluginUpdate {
			for _, spec := range cmd.Specs {
				name := normalizePluginKey(spec)
				if meta, ok := installedPluginMetadata(name); ok {
					beforeVersions[name] = meta.Version
				}
			}
		}

		msg, runErr := runPluginOpWithRecovery(cmd, doneMsg)
		if runErr != nil {
			LogWarning("插件执行失败: %s", runErr)
			setPluginDone(false, runErr.Error())
			return
		}

		// 针对更新操作进行精准陈旧性比对 (Stale Update Detection)
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

		// 卸载成功后清理残留（cordis.patch.yml 用户补丁行与 allowBuilds）
		if cmd.Verb == pluginRemove {
			for _, spec := range cmd.Specs {
				_ = RemovePluginFromProfileUserPatch(cmd.Profile, spec)
			}
			if pluginAllowKey(cmd) != "" {
				_ = cleanupAllowBuildsFor(cmd.Profile, pluginAllowKey(cmd))
			}
		}

		LogInfo("插件执行完成: %s", msg)
		setPluginDone(true, msg)
	}()
}

// validatePluginExecution 校验待执行的插件操作指令合规性
func validatePluginExecution(cmd *pluginCommand) error {
	switch cmd.Verb {
	case pluginAdd:
		for _, spec := range cmd.Specs {
			if err := checkDuplicatePlugin(spec); err != nil {
				return err
			}
		}
	case pluginRemove:
		for _, spec := range cmd.Specs {
			if IsProtectedPlugin(spec) {
				return fmt.Errorf("核心基础设施插件「%s」受到保护，禁止卸载", spec)
			}
		}
	}
	return nil
}

// pluginPreviewError 输入框实时解析校验（输入框限制仅支持 add，并复用底层业务校验）
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
	LogInfo("执行插件指令: %s", cmd.display())

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

// handlePluginToggle 基于官方 cordis.patch.yml 用户补丁层进行热启停
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

	// 官方机制：通过在 cordis.patch.yml 中设置 disabled: true/false
	disabled := !req.Enabled
	if err := SetPluginDisabled("web", req.Name, disabled); err != nil {
		LogWarning("切换插件状态失败 [%s]: %s", req.Name, err)
		Fail(c, http.StatusInternalServerError, "切换插件状态失败: "+err.Error())
		return
	}

	action := "已启用"
	if disabled {
		action = "已禁用"
	}
	msg := fmt.Sprintf("%s插件「%s」", action, req.Name)
	OKMsg(c, msg, gin.H{"name": req.Name, "enabled": req.Enabled})
}
