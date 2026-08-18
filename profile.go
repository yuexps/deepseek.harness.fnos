package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var npmNameStripRe = regexp.MustCompile(`^((?:@[a-z0-9-~][\w.-]*/)?[a-z0-9-~][\w.-]*)@.+$`)

func profileDirFor(name string) string {
	return filepath.Join(pkgVarDir, "dsh-data", "profiles", name)
}

// normalizePluginKey 提取标准包名，去除 npm 版本号
func normalizePluginKey(spec string) string {
	if m := npmNameStripRe.FindStringSubmatch(spec); len(m) >= 2 {
		return m[1]
	}
	return spec
}

// 核心受保护模块正则
var protectedModulePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^cordis:`),
	regexp.MustCompile(`^@deepseek-ai/cordis-plugin-`),
	regexp.MustCompile(`^@deepseek-ai/dsh-host-`),
	regexp.MustCompile(`^@deepseek-ai/dsh-client-`),
	regexp.MustCompile(`^@deepseek-ai/dsh-web`),
	regexp.MustCompile(`^@deepseek-ai/dsh-settings`),
	regexp.MustCompile(`^@deepseek-ai/dsh-credentials`),
	regexp.MustCompile(`^@deepseek-ai/dsh-session`),
	regexp.MustCompile(`^@deepseek-ai/dsh-storage`),
	regexp.MustCompile(`^@deepseek-ai/dsh-tools`),
	regexp.MustCompile(`^@deepseek-ai/dsh-system-prompt`),
	regexp.MustCompile(`^@deepseek-ai/dsh-agent`),
	regexp.MustCompile(`^@deepseek-ai/dsh-llm`),
	regexp.MustCompile(`^@deepseek-ai/dsh-shell`),
	regexp.MustCompile(`^@deepseek-ai/dsh-fs`),
	regexp.MustCompile(`^@deepseek-ai/dsh-sandbox`),
	regexp.MustCompile(`^@deepseek-ai/dsh-jobs`),
	regexp.MustCompile(`^@deepseek-ai/dsh-base`),
	regexp.MustCompile(`^@deepseek-ai/dsh-web-app`),
}

// IsProtectedPlugin 检查是否为受保护的核心基础设施模块
func IsProtectedPlugin(name string) bool {
	if name == "" {
		return false
	}
	for _, p := range protectedModulePatterns {
		if p.MatchString(name) {
			return true
		}
	}
	return false
}

var patchFileMu sync.Mutex

func ProfileUserPatchPath(profile string) string {
	if profile == "" {
		profile = "web"
	}
	return filepath.Join(profileDirFor(profile), "cordis.patch.yml")
}

type CordisPatchRow struct {
	ID       string                 `yaml:"id,omitempty"`
	Name     string                 `yaml:"name,omitempty"`
	Disabled *bool                  `yaml:"disabled,omitempty"`
	Config   map[string]interface{} `yaml:"config,omitempty"`
	Insert   []CordisPatchRow       `yaml:"insert,omitempty"`
}

func ReadProfileUserPatch(profile string) ([]CordisPatchRow, error) {
	patchPath := ProfileUserPatchPath(profile)
	data, err := os.ReadFile(patchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []CordisPatchRow{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []CordisPatchRow{}, nil
	}

	var rows []CordisPatchRow
	if err := yaml.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", patchPath, err)
	}
	return rows, nil
}

func WriteProfileUserPatch(profile string, rows []CordisPatchRow) error {
	patchPath := ProfileUserPatchPath(profile)
	if err := os.MkdirAll(filepath.Dir(patchPath), 0755); err != nil {
		return err
	}

	if len(rows) == 0 {
		return os.WriteFile(patchPath, []byte("[]\n"), 0644)
	}

	data, err := yaml.Marshal(rows)
	if err != nil {
		return fmt.Errorf("序列化 patch 失败: %w", err)
	}
	return os.WriteFile(patchPath, data, 0644)
}

// ExtractPluginEntryIDs 解析插件 bundle patch 声明的 loader entry ID
func ExtractPluginEntryIDs(profile, packageName string) []string {
	var candidates []string

	pkgJsonPath := filepath.Join(profileDirFor(profile), "node_modules", packageName, "package.json")
	data, err := os.ReadFile(pkgJsonPath)
	if err == nil {
		var meta struct {
			Dsh *struct {
				Bundle *struct {
					Patch string `json:"patch"`
				} `json:"bundle"`
			} `json:"dsh"`
		}
		if jsonErr := json.Unmarshal(data, &meta); jsonErr == nil && meta.Dsh != nil && meta.Dsh.Bundle != nil && meta.Dsh.Bundle.Patch != "" {
			patchFile := filepath.Join(filepath.Dir(pkgJsonPath), filepath.FromSlash(meta.Dsh.Bundle.Patch))
			patchData, readErr := os.ReadFile(patchFile)
			if readErr == nil {
				var rows []CordisPatchRow
				if yamlErr := yaml.Unmarshal(patchData, &rows); yamlErr == nil {
					for _, r := range rows {
						if len(r.Insert) > 0 {
							for _, ins := range r.Insert {
								if ins.ID != "" {
									candidates = append(candidates, ins.ID)
								}
							}
						} else if r.ID != "" {
							candidates = append(candidates, r.ID)
						}
					}
				}
			}
		}
	}

	if len(candidates) == 0 {
		candidates = append(candidates, packageName)
	}
	return candidates
}

// ReadDisabledEntryMap 读取指定 profile 中已被禁用的 entry id 集合
func ReadDisabledEntryMap(profile string) (map[string]bool, error) {
	patchFileMu.Lock()
	defer patchFileMu.Unlock()

	rows, err := ReadProfileUserPatch(profile)
	if err != nil {
		return nil, err
	}

	res := make(map[string]bool)
	for _, r := range rows {
		if r.ID != "" && r.Disabled != nil && *r.Disabled {
			res[r.ID] = true
		}
	}
	return res, nil
}

// SetPluginDisabled 在 cordis.patch.yml 中配置插件 entry 的启停状态
func SetPluginDisabled(profile, packageName string, disabled bool) error {
	if IsProtectedPlugin(packageName) {
		return fmt.Errorf("核心基础设施插件 %q 受到保护，禁止更改启停状态", packageName)
	}

	patchFileMu.Lock()
	defer patchFileMu.Unlock()

	entryIDs := ExtractPluginEntryIDs(profile, packageName)
	if len(entryIDs) == 0 {
		entryIDs = []string{packageName}
	}

	rows, err := ReadProfileUserPatch(profile)
	if err != nil {
		return err
	}

	for _, targetID := range entryIDs {
		found := false
		var newRows []CordisPatchRow

		for _, r := range rows {
			if r.ID == targetID {
				found = true
				if disabled {
					val := true
					r.Disabled = &val
					newRows = append(newRows, r)
				} else {
					if len(r.Config) == 0 && r.Name == "" && len(r.Insert) == 0 {
						continue
					}
					r.Disabled = nil
					newRows = append(newRows, r)
				}
			} else {
				newRows = append(newRows, r)
			}
		}

		if !found && disabled {
			val := true
			newRows = append(newRows, CordisPatchRow{
				ID:       targetID,
				Disabled: &val,
			})
		}
		rows = newRows
	}

	if err := WriteProfileUserPatch(profile, rows); err != nil {
		return err
	}

	stateAction := "启用"
	if disabled {
		stateAction = "禁用"
	}
	LogInfo("[Cordis Patch] 已通过 user patch %s 插件 %s (Entry IDs: %v)", stateAction, packageName, entryIDs)
	return nil
}

// RemovePluginFromProfileUserPatch 从 cordis.patch.yml 中物理移除该插件的所有条目
func RemovePluginFromProfileUserPatch(profile, packageName string, entryIDs ...string) error {
	patchFileMu.Lock()
	defer patchFileMu.Unlock()

	rows, err := ReadProfileUserPatch(profile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	idSet := make(map[string]bool)
	idSet[packageName] = true
	for _, id := range entryIDs {
		if id != "" {
			idSet[id] = true
		}
	}

	var newRows []CordisPatchRow
	for _, r := range rows {
		if idSet[r.ID] || idSet[r.Name] {
			continue
		}
		newRows = append(newRows, r)
	}

	return WriteProfileUserPatch(profile, newRows)
}

var (
	blockedBuildsRe = regexp.MustCompile(`(?i)Ignored build scripts:\s*(.+)`)
	pkgNameRe       = regexp.MustCompile(`^(@?[a-zA-Z0-9][\w.-]*(?:/[@a-zA-Z0-9][\w.-]*)?)@[0-9]`)
)

func profileWorkspaceYamlPathFor(name string) string {
	return filepath.Join(profileDirFor(name), "pnpm-workspace.yaml")
}

func allowBuildsSidecarPath() string {
	return filepath.Join(pkgVarDir, "plugins", "allowbuilds.json")
}

// parseBlockedPackages 从 pnpm 错误输出中提取被拦截构建脚本的包名
func parseBlockedPackages(tail string) []string {
	m := blockedBuildsRe.FindStringSubmatch(tail)
	if len(m) < 2 {
		return nil
	}
	var pkgs []string
	for _, part := range strings.Split(m[1], ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if name := pkgNameRe.FindStringSubmatch(part); len(name) >= 2 {
			pkgs = append(pkgs, name[1])
		} else {
			pkgs = append(pkgs, part)
		}
	}
	return pkgs
}

func readAllowBuildsSidecar() map[string][]string {
	m := map[string][]string{}
	data, err := os.ReadFile(allowBuildsSidecarPath())
	if err != nil {
		return m
	}
	_ = json.Unmarshal(data, &m)
	if m == nil {
		m = map[string][]string{}
	}
	return m
}

func writeAllowBuildsSidecar(m map[string][]string) error {
	if err := os.MkdirAll(filepath.Dir(allowBuildsSidecarPath()), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(allowBuildsSidecarPath(), data, 0644)
}

func yamlEntryName(trimmed string) string {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
		return ""
	}
	name := strings.SplitN(trimmed, ":", 2)[0]
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return name
}

// mergeAllowBuildsEntries 合并 allowBuilds 块，保留文件其余内容与注释
func mergeAllowBuildsEntries(yamlPath string, pkgs []string) error {
	content := ""
	if data, err := os.ReadFile(yamlPath); err == nil {
		content = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(content, "\n")

	idx := -1
	entryLine := map[string]int{}
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if idx < 0 {
			if trimmed == "allowBuilds:" || strings.HasPrefix(trimmed, "allowBuilds: ") {
				idx = i
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			if name := yamlEntryName(trimmed); name != "" {
				entryLine[name] = i
			}
			continue
		}
		break
	}

	var missing []string
	var fix []int
	for _, p := range pkgs {
		i, ok := entryLine[p]
		if !ok {
			missing = append(missing, p)
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(lines[i]), ":", 2)
		val := ""
		if len(parts) >= 2 {
			val = strings.TrimSpace(parts[1])
		}
		if val != "true" && val != "false" {
			fix = append(fix, i)
		}
	}
	if len(missing) == 0 && len(fix) == 0 {
		return nil
	}
	sort.Strings(missing)

	if idx < 0 {
		content = strings.TrimRight(content, "\n") + "\n\nallowBuilds:\n"
		for _, p := range missing {
			content += "  " + p + ": true\n"
		}
	} else {
		for _, i := range fix {
			name := yamlEntryName(strings.TrimSpace(lines[i]))
			lines[i] = "  " + name + ": true"
		}
		var out []string
		out = append(out, lines[:idx+1]...)
		for _, p := range missing {
			out = append(out, "  "+p+": true")
		}
		out = append(out, lines[idx+1:]...)
		content = strings.Join(out, "\n")
	}
	if err := os.MkdirAll(filepath.Dir(yamlPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(yamlPath, []byte(content), 0644)
}

// removeAllowBuildsEntries 删除指定包的 allowBuilds 条目
func removeAllowBuildsEntries(yamlPath string, pkgs []string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	drop := map[string]bool{}
	for _, p := range pkgs {
		drop[p] = true
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) &&
			trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "-") {
			parts := strings.SplitN(trimmed, ":", 2)
			name := strings.TrimSpace(parts[0])
			if drop[name] && (len(parts) < 2 || strings.TrimSpace(parts[1]) != "false") {
				continue
			}
		}
		out = append(out, l)
	}
	return os.WriteFile(yamlPath, []byte(strings.Join(out, "\n")), 0644)
}

// ensureAllowBuildsFor 写入 allowBuilds 并记录归属映射
func ensureAllowBuildsFor(profile, pluginKey string, pkgs []string) error {
	if err := mergeAllowBuildsEntries(profileWorkspaceYamlPathFor(profile), pkgs); err != nil {
		return err
	}
	sidecar := readAllowBuildsSidecar()
	for _, p := range pkgs {
		found := false
		for _, k := range sidecar[p] {
			if k == pluginKey {
				found = true
				break
			}
		}
		if !found {
			sidecar[p] = append(sidecar[p], pluginKey)
		}
	}
	return writeAllowBuildsSidecar(sidecar)
}

// cleanupAllowBuildsFor 卸载后移除归属记录并清理孤儿条目
func cleanupAllowBuildsFor(profile, pluginKey string) error {
	sidecar := readAllowBuildsSidecar()
	var orphan []string
	for pkg, keys := range sidecar {
		var keep []string
		for _, k := range keys {
			if k != pluginKey {
				keep = append(keep, k)
			}
		}
		if len(keep) == 0 {
			delete(sidecar, pkg)
			orphan = append(orphan, pkg)
		} else {
			sidecar[pkg] = keep
		}
	}
	if len(orphan) > 0 {
		if err := removeAllowBuildsEntries(profileWorkspaceYamlPathFor(profile), orphan); err != nil {
			return fmt.Errorf("清理 allowBuilds 失败: %s", err)
		}
	}
	return writeAllowBuildsSidecar(sidecar)
}

type PluginFailureInfo struct {
	Target    string    `json:"target"`
	EntryID   string    `json:"entryId,omitempty"`
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

var (
	diagMu                  sync.RWMutex
	failedPlugins           = make(map[string]PluginFailureInfo)
	lastLineWasFailedToLoad bool
)

var (
	loaderEntryErrRe    = regexp.MustCompile(`(?i)failed to apply loader entry\s*([0-9a-fA-F_-]+)?\s*(?:\(([^)]+)\))?:\s*(.+)`)
	genericPluginErrRe  = regexp.MustCompile(`(?i)(?:plugin\s+['"]?([@a-zA-Z0-9/._-]+)['"]?\s+failed|failed to load plugin\s+['"]?([@a-zA-Z0-9/._-]+)['"]?):\s*(.+)`)
	pkgNamePatternRe    = regexp.MustCompile(`^[@a-zA-Z0-9/._-]+$`)
	servicePendingErrRe = regexp.MustCompile(`(?i)([@a-zA-Z0-9/._-]+):\s*pending\s*\(waiting for service:\s*([^)]+)\)`)
)

func RecordPluginFailureRecord(pkgName, entryID, reason string) {
	pkgName = strings.TrimSpace(pkgName)
	entryID = strings.TrimSpace(entryID)
	reason = strings.TrimSpace(reason)

	if pkgName == "" && entryID == "" {
		return
	}

	diagMu.Lock()
	defer diagMu.Unlock()

	key := pkgName
	if key == "" {
		key = entryID
	}

	failedPlugins[key] = PluginFailureInfo{
		Target:    pkgName,
		EntryID:   entryID,
		Reason:    reason,
		Timestamp: time.Now(),
	}
	LogWarning("[插件故障捕获] 成功捕获插件崩溃: %s (Entry: %s) -> %s", pkgName, entryID, reason)
}

func ClearPluginFailure(pkgName string) {
	diagMu.Lock()
	defer diagMu.Unlock()
	delete(failedPlugins, pkgName)
}

func ClearAllPluginFailures() {
	diagMu.Lock()
	defer diagMu.Unlock()
	lastLineWasFailedToLoad = false
	if len(failedPlugins) > 0 {
		failedPlugins = make(map[string]PluginFailureInfo)
	}
}

func GetFailedPlugins() map[string]PluginFailureInfo {
	diagMu.RLock()
	defer diagMu.RUnlock()
	res := make(map[string]PluginFailureInfo, len(failedPlugins))
	for k, v := range failedPlugins {
		res[k] = v
	}
	return res
}

func DisableAllBrokenPlugins(profile string) ([]string, error) {
	failed := GetFailedPlugins()
	if len(failed) == 0 {
		return nil, nil
	}

	var disabledNames []string
	for name := range failed {
		if IsProtectedPlugin(name) {
			continue
		}
		if err := SetPluginDisabled(profile, name, true); err != nil {
			LogWarning("[一键自愈] 禁用故障插件 %s 失败: %s", name, err)
			continue
		}
		ClearPluginFailure(name)
		disabledNames = append(disabledNames, name)
	}

	LogInfo("[一键自愈] 已批量禁用故障插件: %v", disabledNames)
	return disabledNames, nil
}

// ParseAndRecordStderrDiagnostics 解析 stderr/stdout 中的插件崩溃信息
func ParseAndRecordStderrDiagnostics(text string) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}

	if m := loaderEntryErrRe.FindStringSubmatch(clean); len(m) >= 4 {
		entryID := strings.TrimSpace(m[1])
		pkgName := strings.TrimSpace(m[2])
		reason := strings.TrimSpace(m[3])
		if pkgName == "" {
			pkgName = entryID
		}
		RecordPluginFailureRecord(pkgName, entryID, reason)
		lastLineWasFailedToLoad = false
		return
	}

	if m := genericPluginErrRe.FindStringSubmatch(clean); len(m) >= 4 {
		pkgName := strings.TrimSpace(m[1])
		if pkgName == "" {
			pkgName = strings.TrimSpace(m[2])
		}
		reason := strings.TrimSpace(m[3])
		RecordPluginFailureRecord(pkgName, "", reason)
		lastLineWasFailedToLoad = false
		return
	}

	if m := servicePendingErrRe.FindStringSubmatch(clean); len(m) >= 3 {
		target := strings.TrimSpace(m[1])
		svc := strings.TrimSpace(m[2])
		RecordPluginFailureRecord(target, "", fmt.Sprintf("服务挂起: 等待 %s 超时 (前置插件崩溃引发连锁中断)", svc))
		lastLineWasFailedToLoad = false
		return
	}

	if strings.EqualFold(clean, "Failed to load plugins") || strings.EqualFold(clean, "Failed to load plugin") {
		lastLineWasFailedToLoad = true
		return
	}

	if lastLineWasFailedToLoad {
		if pkgNamePatternRe.MatchString(clean) {
			RecordPluginFailureRecord(clean, "", "启动时加载失败 (Failed to load plugin)")
		}
		lastLineWasFailedToLoad = false
	}
}
