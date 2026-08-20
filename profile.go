package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

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

// ResetAllProfilePatches 清空 Profile 目录与插件白名单缓存，由主程序按模板重新初始化
func ResetAllProfilePatches() {
	patchFileMu.Lock()
	defer patchFileMu.Unlock()

	_ = safeRemoveAll(filepath.Join(pkgVarDir, "dsh-data", "profiles"))
	_ = safeRemoveAll(filepath.Join(pkgVarDir, "plugins"))
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

// PnpmFailureCode pnpm 故障分类类型
type PnpmFailureCode string

const (
	PnpmFailureHoistPatternDiff PnpmFailureCode = "hoist-pattern-diff"
	PnpmFailureReleaseAge       PnpmFailureCode = "release-age-violation"
	PnpmFailureFetchTimeout     PnpmFailureCode = "fetch-timeout"
	PnpmFailureTransientNetwork PnpmFailureCode = "transient-network"
	PnpmFailureIgnoredBuilds    PnpmFailureCode = "ignored-builds"
	PnpmFailureGitDepPrepare    PnpmFailureCode = "git-prepare-not-allowed"
	PnpmFailureFetch404         PnpmFailureCode = "fetch-404"
	PnpmFailureAddingToRoot     PnpmFailureCode = "adding-to-root"
	PnpmFailureUnexpectedStore  PnpmFailureCode = "unexpected-store"
	PnpmFailureUnknown          PnpmFailureCode = "unknown"
)

// PnpmFailureInfo 识别出的故障详情
type PnpmFailureInfo struct {
	Code        PnpmFailureCode
	Recoverable bool
	Message     string
	DetailPkg   string
}

var (
	re404Pkg       = regexp.MustCompile(`(?:GET|fetch)\s+\S*\/([^/\s:]+)(?::|\s)`)
	reTransientNet = regexp.MustCompile(`(?i)(?:ERR_PNPM_FETCH_5\d\d|ERR_PNPM_META_FETCH_FAIL|FetchError|ECONNRESET|ETIMEDOUT|EAI_AGAIN|ENETUNREACH|socket hang up|network timeout)`)
	reFetchTimeout = regexp.MustCompile(`(?i)(?:operation was aborted due to timeout|TimeoutError|error \(23\))`)
	semverPattern  = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)
)

// ClassifyPnpmFailure 智能分类 pnpm 执行失败原因
func ClassifyPnpmFailure(output string) PnpmFailureInfo {
	if strings.Contains(output, "ERR_PNPM_PUBLIC_HOIST_PATTERN_DIFF") {
		return PnpmFailureInfo{
			Code:        PnpmFailureHoistPatternDiff,
			Recoverable: true,
			Message:     "node_modules 是旧版 pnpm 创建的，存在依赖结构差异，已自动重建后重试",
		}
	}

	if strings.Contains(output, "ERR_PNPM_UNEXPECTED_STORE") {
		return PnpmFailureInfo{
			Code:        PnpmFailureUnexpectedStore,
			Recoverable: true,
			Message:     "依赖存储位置变更，已自动清理缓存并重试",
		}
	}

	if strings.Contains(output, "ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION") ||
		strings.Contains(output, "ERR_PNPM_NO_MATURE_MATCHING_VERSION") {
		return PnpmFailureInfo{
			Code:        PnpmFailureReleaseAge,
			Recoverable: true,
			Message:     "检测到刚发布的新版本受 pnpm 安全期限制，已自动放行并重试",
		}
	}

	if reFetchTimeout.MatchString(output) {
		return PnpmFailureInfo{
			Code:        PnpmFailureFetchTimeout,
			Recoverable: true,
			Message:     "下载耗时超出默认限制，已自动延长超时时间并重试",
		}
	}

	if strings.Contains(output, "ERR_PNPM_IGNORED_BUILDS") {
		return PnpmFailureInfo{
			Code:        PnpmFailureIgnoredBuilds,
			Recoverable: true,
			Message:     "依赖包含构建脚本，已被 pnpm 默认拦截，已自动配置放行并重试",
		}
	}

	if strings.Contains(output, "ERR_PNPM_GIT_DEP_PREPARE_NOT_ALLOWED") {
		return PnpmFailureInfo{
			Code:        PnpmFailureGitDepPrepare,
			Recoverable: true,
			Message:     "Git 插件包含构建脚本，已自动配置放行并重试",
		}
	}

	if strings.Contains(output, "ERR_PNPM_FETCH_404") {
		detailPkg := ""
		if m := re404Pkg.FindStringSubmatch(output); len(m) > 1 {
			detailPkg = strings.ReplaceAll(m[1], "%2F", "/")
			detailPkg = strings.ReplaceAll(detailPkg, "%2f", "/")
		}
		msg := "指定的插件包在 npm 镜像源上不存在 (404)"
		if detailPkg != "" {
			msg = fmt.Sprintf("依赖包「%s」在镜像源上不存在 (404)，可能未发布或存在历史残留", detailPkg)
		}
		return PnpmFailureInfo{
			Code:        PnpmFailureFetch404,
			Recoverable: false,
			Message:     msg,
			DetailPkg:   detailPkg,
		}
	}

	if reTransientNet.MatchString(output) {
		return PnpmFailureInfo{
			Code:        PnpmFailureTransientNetwork,
			Recoverable: true,
			Message:     "网络连接瞬态抖动，已自动重试",
		}
	}

	return PnpmFailureInfo{
		Code:        PnpmFailureUnknown,
		Recoverable: false,
		Message:     "插件指令执行失败",
	}
}

// FormatPnpmFailureMessage 将底层复杂的报错提炼为精炼的中文反馈
func FormatPnpmFailureMessage(output string) string {
	info := ClassifyPnpmFailure(output)
	if info.Code == PnpmFailureFetch404 {
		return info.Message
	}

	// 提取最具参考价值的单行错误
	lines := strings.Split(output, "\n")
	var meaningfulLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "ERR_PNPM_") ||
			strings.HasPrefix(trimmed, "npm ERR!") ||
			strings.HasPrefix(trimmed, "error:") ||
			strings.Contains(trimmed, "Error:") {
			meaningfulLines = append(meaningfulLines, trimmed)
		}
	}

	if len(meaningfulLines) > 0 {
		return fmt.Sprintf("%s（%s）", info.Message, meaningfulLines[0])
	}

	// 兜底截取最后一段有效文字
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t != "" && !strings.HasPrefix(t, "at ") {
			if len(t) > 120 {
				t = t[:120] + "…"
			}
			return fmt.Sprintf("%s: %s", info.Message, t)
		}
	}
	return info.Message
}

// parsedSemver 结构化 Semver
type parsedSemver struct {
	Major int
	Minor int
	Patch int
	Pre   []string
}

func parseSemver(v string) (parsedSemver, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "^")
	v = strings.TrimPrefix(v, "~")

	m := semverPattern.FindStringSubmatch(v)
	if len(m) == 0 {
		return parsedSemver{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])

	var pre []string
	if m[4] != "" {
		pre = strings.Split(m[4], ".")
	}
	return parsedSemver{Major: maj, Minor: min, Patch: pat, Pre: pre}, true
}

// CompareSemver 严格语义化版本比较: v1 > v2 返回 1; v1 < v2 返回 -1; 相等返回 0
func CompareSemver(v1, v2 string) int {
	p1, ok1 := parseSemver(v1)
	p2, ok2 := parseSemver(v2)
	if !ok1 || !ok2 {
		if v1 == v2 {
			return 0
		}
		return strings.Compare(v1, v2)
	}

	if p1.Major != p2.Major {
		if p1.Major > p2.Major {
			return 1
		}
		return -1
	}
	if p1.Minor != p2.Minor {
		if p1.Minor > p2.Minor {
			return 1
		}
		return -1
	}
	if p1.Patch != p2.Patch {
		if p1.Patch > p2.Patch {
			return 1
		}
		return -1
	}

	// 正式版本优于预览版
	if len(p1.Pre) == 0 && len(p2.Pre) > 0 {
		return 1
	}
	if len(p1.Pre) > 0 && len(p2.Pre) == 0 {
		return -1
	}
	if len(p1.Pre) == 0 && len(p2.Pre) == 0 {
		return 0
	}

	// 逐段比较 pre-release
	maxLen := len(p1.Pre)
	if len(p2.Pre) > maxLen {
		maxLen = len(p2.Pre)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(p1.Pre) {
			return -1
		}
		if i >= len(p2.Pre) {
			return 1
		}
		s1 := p1.Pre[i]
		s2 := p2.Pre[i]
		if s1 == s2 {
			continue
		}
		n1, err1 := strconv.Atoi(s1)
		n2, err2 := strconv.Atoi(s2)
		if err1 == nil && err2 == nil {
			if n1 > n2 {
				return 1
			}
			return -1
		}
		if err1 == nil && err2 != nil {
			return -1
		}
		if err1 != nil && err2 == nil {
			return 1
		}
		return strings.Compare(s1, s2)
	}
	return 0
}

