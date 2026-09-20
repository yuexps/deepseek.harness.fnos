package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	snapshotMu     sync.Mutex
	validIDRegex   = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	snapshotSubs   = make(map[chan struct{}]struct{})
	snapshotSubsMu sync.Mutex

	snapProgressSubs   = make(map[chan SnapshotProgress]struct{})
	snapProgressSubsMu sync.Mutex

	currentSnapshotProgress SnapshotProgress
	currentSnapshotMu       sync.RWMutex

	// snapshotArchiveTargets 定义快照打包与还原的目标数据清单
	snapshotArchiveTargets = []string{"config.json", "dsh-runtime", "home", "dsh-data"}
)

// SnapshotProgress 快照进度事件与状态
type SnapshotProgress struct {
	Active  bool   `json:"active"`
	Action  string `json:"action"`
	Percent int    `json:"percent"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// GetCurrentSnapshotProgress 获取当前快照任务实时进度
func GetCurrentSnapshotProgress() SnapshotProgress {
	currentSnapshotMu.RLock()
	defer currentSnapshotMu.RUnlock()
	return currentSnapshotProgress
}

func setSnapshotProgress(p SnapshotProgress) {
	currentSnapshotMu.Lock()
	currentSnapshotProgress = p
	currentSnapshotMu.Unlock()
	broadcastSnapshotProgress(p)
}

// failSnapshotProgress 广播快照任务失败状态
func failSnapshotProgress(action, stage string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	currentSnapshotMu.Lock()
	p := SnapshotProgress{
		Active:  false,
		Action:  action,
		Percent: 0,
		Stage:   stage,
		Message: msg,
		Error:   msg,
	}
	currentSnapshotProgress = p
	currentSnapshotMu.Unlock()
	broadcastSnapshotProgress(p)
}

func clearSnapshotProgress() {
	currentSnapshotMu.Lock()
	currentSnapshotProgress = SnapshotProgress{Active: false}
	currentSnapshotMu.Unlock()
	broadcastSnapshotProgress(SnapshotProgress{Active: false})
}

// SubscribeSnapshotProgress 订阅快照实际进度
func SubscribeSnapshotProgress(buf int) (<-chan SnapshotProgress, func()) {
	snapProgressSubsMu.Lock()
	defer snapProgressSubsMu.Unlock()
	ch := make(chan SnapshotProgress, buf)
	snapProgressSubs[ch] = struct{}{}
	return ch, func() {
		snapProgressSubsMu.Lock()
		delete(snapProgressSubs, ch)
		snapProgressSubsMu.Unlock()
	}
}

func broadcastSnapshotProgress(p SnapshotProgress) {
	snapProgressSubsMu.Lock()
	subs := make([]chan SnapshotProgress, 0, len(snapProgressSubs))
	for ch := range snapProgressSubs {
		subs = append(subs, ch)
	}
	snapProgressSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- p:
		default:
		}
	}
}

// SubscribeSnapshot 订阅快照变更事件
func SubscribeSnapshot(buf int) (<-chan struct{}, func()) {
	snapshotSubsMu.Lock()
	defer snapshotSubsMu.Unlock()
	ch := make(chan struct{}, buf)
	snapshotSubs[ch] = struct{}{}
	return ch, func() {
		snapshotSubsMu.Lock()
		delete(snapshotSubs, ch)
		snapshotSubsMu.Unlock()
	}
}

func notifySnapshot() {
	snapshotSubsMu.Lock()
	subs := make([]chan struct{}, 0, len(snapshotSubs))
	for ch := range snapshotSubs {
		subs = append(subs, ch)
	}
	snapshotSubsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// DiskUsage 磁盘容量统计
type DiskUsage struct {
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
}

// MemInfo 内存统计
type MemInfo struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	SwapFreeBytes  uint64 `json:"swap_free_bytes"`
}

// CPUInfo CPU与系统负载
type CPUInfo struct {
	Cores     int     `json:"cores"`
	ModelName string  `json:"model_name"`
	Load1     float64 `json:"load1"`
}

func getDiskUsage(path string) (DiskUsage, error) {
	if path == "" {
		path = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return DiskUsage{}, err
	}

	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize
	used := uint64(0)
	if total >= stat.Bfree*bsize {
		used = total - stat.Bfree*bsize
	}

	return DiskUsage{
		TotalBytes: total,
		FreeBytes:  free,
		UsedBytes:  used,
	}, nil
}

func getMemoryInfo() (MemInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemInfo{}, err
	}
	defer f.Close()

	var total, avail, swapFree uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		bytes := val * 1024 // /proc/meminfo 单位为 kB

		switch parts[0] {
		case "MemTotal:":
			total = bytes
		case "MemAvailable:":
			avail = bytes
		case "SwapFree:":
			swapFree = bytes
		}
	}
	if err := scanner.Err(); err != nil {
		return MemInfo{}, err
	}

	return MemInfo{
		TotalBytes:     total,
		AvailableBytes: avail,
		SwapFreeBytes:  swapFree,
	}, nil
}

func getCPUInfo() (CPUInfo, error) {
	info := CPUInfo{Cores: 1}

	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		defer f.Close()
		cores := 0
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "processor") {
				cores++
			} else if strings.HasPrefix(line, "model name") && info.ModelName == "" {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					info.ModelName = strings.TrimSpace(parts[1])
				}
			}
		}
		_ = scanner.Err()
		if cores > 0 {
			info.Cores = cores
		}
	}

	if loadData, err := os.ReadFile("/proc/loadavg"); err == nil {
		parts := strings.Fields(string(loadData))
		if len(parts) >= 1 {
			if l, err := strconv.ParseFloat(parts[0], 64); err == nil {
				info.Load1 = l
			}
		}
	}

	return info, nil
}

const (
	// MinSnapshotDiskFreeBytes 创建快照最小空闲磁盘要求 (1GB)
	MinSnapshotDiskFreeBytes uint64 = 1024 * 1024 * 1024
	// MinMemAvailableBytes 执行快照任务最低可用内存 (256MB 防 OOM)
	MinMemAvailableBytes uint64 = 256 * 1024 * 1024
)

// checkSnapshotResource 检查快照所需的系统资源（动态磁盘与安全内存）
func checkSnapshotResource(requiredDisk uint64) error {
	disk, err := getDiskUsage(globalPkgVar)
	if err == nil && disk.FreeBytes < requiredDisk {
		return fmt.Errorf("磁盘可用空间不足 (当前: %s, 要求: >= %s)，请先清理存储空间", formatBytes(disk.FreeBytes), formatBytes(requiredDisk))
	}

	mem, err := getMemoryInfo()
	if err == nil && mem.AvailableBytes < MinMemAvailableBytes {
		return fmt.Errorf("系统可用内存过低 (当前: %s, 要求: >= %s)，请释放部分内存后再操作", formatBytes(mem.AvailableBytes), formatBytes(MinMemAvailableBytes))
	}

	if cpu, err := getCPUInfo(); err == nil && cpu.Cores > 0 && cpu.Load1 > float64(cpu.Cores)*2.5 {
		LogWarning("[快照] 当前系统 CPU 负载较高 (1分钟负载: %.2f, %d 核)，快照任务可能耗时较长", cpu.Load1, cpu.Cores)
	}

	return nil
}

// CheckResourceForSnapshot 创建快照前资源检查
func CheckResourceForSnapshot() error {
	return checkSnapshotResource(MinSnapshotDiskFreeBytes)
}

// CheckResourceForRestore 还原快照前资源检查
func CheckResourceForRestore(snapshotSizeBytes int64) error {
	requiredDisk := uint64(snapshotSizeBytes)*2 + 300*1024*1024
	if requiredDisk < 500*1024*1024 {
		requiredDisk = 500 * 1024 * 1024
	}
	return checkSnapshotResource(requiredDisk)
}

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// SnapshotMeta 快照元数据
type SnapshotMeta struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	CreatedAt        int64  `json:"created_at"`
	SizeBytes        int64  `json:"size_bytes"`
	AppVersion       string `json:"app_version"`
	HarnessVersion   string `json:"harness_version"`
	PluginCount      int    `json:"plugin_count"`
	CompressionLevel int    `json:"compression_level,omitempty"`
}

// CreateSnapshotParams 创建快照参数
type CreateSnapshotParams struct {
	Name             string `json:"name"`
	CompressionLevel int    `json:"compression_level"`
}

// SnapshotSummary 快照列表与存储汇总
type SnapshotSummary struct {
	Items          []SnapshotMeta   `json:"items"`
	TotalSizeBytes int64            `json:"total_size_bytes"`
	DiskFreeBytes  uint64           `json:"disk_free_bytes"`
	DiskTotalBytes uint64           `json:"disk_total_bytes"`
	CurrentTask    SnapshotProgress `json:"current_task"`
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ListSnapshots 获取所有快照列表及统计
func ListSnapshots() (SnapshotSummary, error) {
	base := globalSnapshotsDir
	_ = os.MkdirAll(base, 0755)

	entries, err := os.ReadDir(base)
	if err != nil {
		return SnapshotSummary{Items: []SnapshotMeta{}, CurrentTask: GetCurrentSnapshotProgress()}, err
	}

	var items []SnapshotMeta
	var totalSize int64

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if !validIDRegex.MatchString(id) {
			continue
		}
		metaFile := filepath.Join(base, id, "meta.json")
		data, err := os.ReadFile(metaFile)
		if err != nil {
			continue
		}
		var meta SnapshotMeta
		if err := json.Unmarshal(data, &meta); err == nil && meta.ID != "" {
			items = append(items, meta)
			totalSize += meta.SizeBytes
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})

	disk, _ := getDiskUsage(globalPkgVar)

	return SnapshotSummary{
		Items:          items,
		TotalSizeBytes: totalSize,
		DiskFreeBytes:  disk.FreeBytes,
		DiskTotalBytes: disk.TotalBytes,
		CurrentTask:    GetCurrentSnapshotProgress(),
	}, nil
}

// CreateSnapshot 创建新快照
func CreateSnapshot(params CreateSnapshotParams) (retMeta *SnapshotMeta, err error) {
	cur := state.Status()
	if cur == StatusBuilding {
		return nil, fmt.Errorf("服务正在部署更新中，请稍候再试")
	}
	if cur == StatusSnapshotting {
		return nil, fmt.Errorf("已有快照任务正在执行中，请勿重复操作")
	}
	if cur == StatusStarting {
		return nil, fmt.Errorf("服务正在启动中，请等待启动完成后再创建快照")
	}

	targetName := strings.TrimSpace(params.Name)
	if targetName == "" {
		targetName = "手动快照_" + time.Now().Format("0102_1504")
	}
	params.Name = targetName

	if err := CheckResourceForSnapshot(); err != nil {
		return nil, err
	}

	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	// 检查已有快照名称，拦截重名
	base := globalSnapshotsDir
	if entries, err := os.ReadDir(base); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !validIDRegex.MatchString(entry.Name()) {
				continue
			}
			metaFile := filepath.Join(base, entry.Name(), "meta.json")
			if data, err := os.ReadFile(metaFile); err == nil {
				var m SnapshotMeta
				if err := json.Unmarshal(data, &m); err == nil {
					if strings.EqualFold(strings.TrimSpace(m.Name), targetName) {
						return nil, fmt.Errorf("已存在同名快照「%s」，请更换名称", targetName)
					}
				}
			}
		}
	}

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "create",
		Percent: 0,
		Stage:   "正在准备快照环境",
		Message: "停止服务进程与校验系统环境",
	})
	defer func() {
		if err != nil {
			LogError("[快照] 创建快照失败: %s", err)
			failSnapshotProgress("create", "创建快照失败", err)
			go func() {
				time.Sleep(3 * time.Second)
				clearSnapshotProgress()
			}()
			return
		}
		go func() {
			time.Sleep(1500 * time.Millisecond)
			clearSnapshotProgress()
		}()
	}()

	// 记录服务运行状态
	wasRunning := (state.Status() == StatusRunning)
	if wasRunning {
		LogInfo("[快照] 停止运行中服务主进程，刷新持久化数据落盘")
		state.SetStatus(StatusSnapshotting, "准备创建系统快照...")
		KillHarness()
		time.Sleep(1 * time.Second)
	}

	id := fmt.Sprintf("snap_%s_%s", time.Now().Format("20060102_150405"), randHex(3))
	snapDir := filepath.Join(globalSnapshotsDir, id)
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		if wasRunning {
			state.SetStatus(StatusStopped, "")
			_ = Start()
		}
		return nil, fmt.Errorf("创建快照目录失败: %w", err)
	}

	tarPath := filepath.Join(snapDir, "data.tar.gz")

	pluginCount := 0
	if m, err := readProfileManifestFile(pluginProfileDir()); err == nil {
		pluginCount = len(m.Dependencies)
	}

	harnessVer := readVersion()

	level := params.CompressionLevel
	if level < 1 || level > 9 {
		level = gzip.BestSpeed
	}

	meta := SnapshotMeta{
		ID:               id,
		Name:             strings.TrimSpace(params.Name),
		CreatedAt:        time.Now().Unix(),
		AppVersion:       globalAppVer,
		HarnessVersion:   harnessVer,
		PluginCount:      pluginCount,
		CompressionLevel: level,
	}

	LogInfo("[快照] 开始打包快照 [%s]: 名称=\"%s\", 压缩级别=Lv%d, 插件数=%d", id, meta.Name, level, pluginCount)
	snapStart := time.Now()
	if err := archiveSnapshotData(tarPath, level); err != nil {
		_ = os.RemoveAll(snapDir)
		if wasRunning {
			state.SetStatus(StatusStopped, "")
			_ = Start()
		}
		return nil, fmt.Errorf("快照打包失败: %w", err)
	}

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "create",
		Percent: 95,
		Stage:   "正在保存元数据与恢复服务",
		Message: "写入快照描述并自启服务",
	})

	fi, err := os.Stat(tarPath)
	if err == nil {
		meta.SizeBytes = fi.Size()
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(filepath.Join(snapDir, "meta.json"), metaBytes, 0644)

	// 打包完成后，若之前为运行中则自动拉起恢复服务
	if wasRunning {
		LogInfo("[快照] 打包归档完成 (耗时=%s)，拉起恢复服务", time.Since(snapStart).Round(time.Millisecond))
		state.SetStatus(StatusStopped, "")
		if err := Start(); err != nil {
			LogWarning("[快照] 创建后服务自启失败: %s", err)
		}
	}

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "create",
		Percent: 100,
		Stage:   "快照创建完成",
		Message: fmt.Sprintf("快照已就绪 (%s)", formatBytes(uint64(meta.SizeBytes))),
	})

	LogInfo("[快照] 快照 [%s] 创建成功 (归档大小=%s, 总耗时=%s)", id, formatBytes(uint64(meta.SizeBytes)), time.Since(snapStart).Round(time.Millisecond))
	notifySnapshot()
	return &meta, nil
}

type countWriter struct {
	w io.Writer
	n *int64
}

func (cw *countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 && cw.n != nil {
		*cw.n += int64(n)
	}
	return n, err
}

type progressWriter struct {
	w          io.Writer
	onProgress func(int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 && pw.onProgress != nil {
		pw.onProgress(int64(n))
	}
	return n, err
}

// archiveSnapshotData 将全量目标模块打包至 tar.gz 并广播实际进度
func archiveSnapshotData(tarPath string, level int) error {
	targets := snapshotArchiveTargets

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "create",
		Percent: 0,
		Stage:   "正在统计数据总量",
		Message: "扫描源文件元信息",
	})

	LogInfo("[快照] 扫描待归档数据源: [%s]", strings.Join(targets, ", "))
	scanStart := time.Now()
	var totalBytes int64
	var fileCount int
	for _, rel := range targets {
		p := filepath.Join(globalPkgVar, rel)
		_ = filepath.Walk(p, func(_ string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode().IsRegular() {
				totalBytes += fi.Size()
				fileCount++
			}
			return nil
		})
	}
	if totalBytes <= 0 {
		totalBytes = 1
	}
	LogInfo("[快照] 数据源预检完成: 文件数=%d, 原始大小=%s (耗时=%s)", fileCount, formatBytes(uint64(totalBytes)), time.Since(scanStart).Round(time.Millisecond))

	f, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if level < 1 || level > 9 {
		level = gzip.BestSpeed
	}

	var compressedBytes int64
	cw := &countWriter{w: f, n: &compressedBytes}
	gw, err := gzip.NewWriterLevel(cw, level)
	if err != nil {
		return err
	}
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	var processedBytes int64
	var lastReportPct = -1
	var lastReportTime time.Time
	var lastMilestonePct = 0

	reportProgress := func(written int64) {
		processedBytes += written
		// 压缩流占整个快照生命周期的 5% ~ 90%
		pct := 5 + int(float64(processedBytes)*85/float64(totalBytes))
		if pct > 90 {
			pct = 90
		}
		now := time.Now()
		if pct != lastReportPct && now.Sub(lastReportTime) >= 120*time.Millisecond {
			lastReportPct = pct
			lastReportTime = now
			setSnapshotProgress(SnapshotProgress{
				Active:  true,
				Action:  "create",
				Percent: pct,
				Stage:   "正在打包压缩",
				Message: fmt.Sprintf("%s / %s · 已压缩输出 %s", formatBytes(uint64(processedBytes)), formatBytes(uint64(totalBytes)), formatBytes(uint64(compressedBytes))),
			})
		}
		// 关键进度里程碑输出日志
		if pct >= lastMilestonePct+25 && pct < 90 {
			lastMilestonePct = (pct / 25) * 25
			LogInfo("[快照] 归档压缩进度 %d%% (读取 %s/%s, 写入 %s)", pct, formatBytes(uint64(processedBytes)), formatBytes(uint64(totalBytes)), formatBytes(uint64(compressedBytes)))
		}
	}

	pw := &progressWriter{w: tw, onProgress: reportProgress}

	packStart := time.Now()
	for _, rel := range targets {
		srcPath := filepath.Join(globalPkgVar, rel)
		fi, err := os.Lstat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		if !fi.IsDir() {
			if err := addFileToTar(tw, pw, globalPkgVar, rel, fi); err != nil {
				return err
			}
			continue
		}

		err = filepath.Walk(srcPath, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relPath, err := filepath.Rel(globalPkgVar, path)
			if err != nil {
				return err
			}
			relPath = filepath.ToSlash(relPath)
			return addFileToTar(tw, pw, globalPkgVar, relPath, info)
		})
		if err != nil {
			return err
		}
	}

	// 显式完成压缩落盘，获取最终压缩文件大小
	_ = tw.Close()
	_ = gw.Close()

	ratio := 100.0
	if totalBytes > 0 {
		ratio = float64(compressedBytes) * 100 / float64(totalBytes)
	}
	LogInfo("[快照] 数据压缩落盘完成: 原始大小=%s, 压缩大小=%s (压缩比=%.1f%%, 耗时=%s)",
		formatBytes(uint64(totalBytes)),
		formatBytes(uint64(compressedBytes)),
		ratio,
		time.Since(packStart).Round(time.Millisecond),
	)

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "create",
		Percent: 90,
		Stage:   "压缩归档完成",
		Message: fmt.Sprintf("原始 %s → 压缩后 %s", formatBytes(uint64(totalBytes)), formatBytes(uint64(compressedBytes))),
	})

	return nil
}

func addFileToTar(tw *tar.Writer, pw io.Writer, baseDir, relPath string, info os.FileInfo) error {
	// 仅归档常规文件、目录与软链接，跳过特殊文件
	mode := info.Mode()
	if !mode.IsRegular() && !mode.IsDir() && (mode&os.ModeSymlink == 0) {
		return nil
	}

	fullPath := filepath.Join(baseDir, filepath.FromSlash(relPath))

	var linkTarget string
	var err error
	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, err = os.Readlink(fullPath)
		if err != nil {
			return err
		}
	}

	hdr, err := tar.FileInfoHeader(info, linkTarget)
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(relPath)

	if info.IsDir() {
		hdr.Name += "/"
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	if !info.Mode().IsRegular() {
		return nil
	}

	file, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(pw, file)
	return err
}

func verifySnapshotArchive(tarPath string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip 格式损坏: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	hdr, err := tr.Next()
	if err != nil {
		if err == io.EOF {
			return fmt.Errorf("快照包内无有效文件")
		}
		return fmt.Errorf("tar 结构异常: %w", err)
	}
	if hdr.Name == "" {
		return fmt.Errorf("快照包内无有效文件")
	}
	return nil
}

// ValidateSnapshotForRestore 还原前校验快照有效性与系统资源
func ValidateSnapshotForRestore(id string) (*SnapshotMeta, error) {
	cur := state.Status()
	if cur == StatusBuilding {
		return nil, fmt.Errorf("服务正在部署更新中，无法还原快照，请稍候再试")
	}
	if cur == StatusSnapshotting {
		return nil, fmt.Errorf("已有快照任务正在执行中，请勿重复操作")
	}

	if !validIDRegex.MatchString(id) {
		return nil, fmt.Errorf("非法快照 ID: %s", id)
	}

	snapDir := filepath.Join(globalSnapshotsDir, id)
	metaFile := filepath.Join(snapDir, "meta.json")
	metaData, err := os.ReadFile(metaFile)
	if err != nil {
		return nil, fmt.Errorf("快照元数据不存在")
	}

	var meta SnapshotMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("解析快照元数据失败: %w", err)
	}

	if meta.AppVersion == "" || CompareSemver(meta.AppVersion, "0.3.1") < 0 {
		return nil, fmt.Errorf("该快照生成自旧版源码架构 (v%s < v0.3.1)，已不兼容当前版本，无法还原", meta.AppVersion)
	}

	tarPath := filepath.Join(snapDir, "data.tar.gz")
	if _, err := os.Stat(tarPath); err != nil {
		return nil, fmt.Errorf("快照压缩包缺失")
	}

	if err := CheckResourceForRestore(meta.SizeBytes); err != nil {
		return nil, err
	}

	return &meta, nil
}

// RestoreSnapshot 还原指定快照
func RestoreSnapshot(id string) (err error) {
	meta, err := ValidateSnapshotForRestore(id)
	if err != nil {
		return err
	}

	tarPath := filepath.Join(globalSnapshotsDir, id, "data.tar.gz")

	LogInfo("[快照] 开始执行快照还原 [%s]: 名称=\"%s\", 版本=v%s, 归档大小=%s", id, meta.Name, meta.HarnessVersion, formatBytes(uint64(meta.SizeBytes)))

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "restore",
		Percent: 5,
		Stage:   "正在校验快照数据完整性",
		Message: fmt.Sprintf("快照: %s", meta.Name),
	})
	defer func() {
		if err != nil {
			LogError("[快照] 还原快照失败: %s", err)
			failSnapshotProgress("restore", "还原快照失败", err)
			go func() {
				time.Sleep(3 * time.Second)
				clearSnapshotProgress()
			}()
			return
		}
		go func() {
			time.Sleep(1500 * time.Millisecond)
			clearSnapshotProgress()
		}()
	}()

	LogInfo("[快照] 开始校验归档包数据完整性")
	verifyStart := time.Now()
	if err := verifySnapshotArchive(tarPath); err != nil {
		LogError("[快照] 归档包数据校验失败: %s", err)
		return fmt.Errorf("快照校验失败，取消还原: %w", err)
	}
	LogInfo("[快照] 归档包校验通过 (耗时=%s)", time.Since(verifyStart).Round(time.Millisecond))

	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	restoreStart := time.Now()
	state.SetStatus(StatusSnapshotting, "准备还原系统快照...")
	LogInfo("[快照] 停止当前运行中服务")
	KillHarness()

	trashDir := filepath.Join(globalPkgVar, fmt.Sprintf("_trash_restore_%s", time.Now().Format("20060102_150405")))
	_ = os.MkdirAll(trashDir, 0755)

	targetDirs := snapshotArchiveTargets
	var movedDirs []string

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "restore",
		Percent: 20,
		Stage:   "正在停止服务并隔离现有数据",
		Message: "准备解压运行环境",
	})

	LogInfo("[快照] 备份当前工作区数据至临时隔离目录 [%s]", filepath.Base(trashDir))
	for _, d := range targetDirs {
		src := filepath.Join(globalPkgVar, d)
		dst := filepath.Join(trashDir, d)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err == nil {
				movedDirs = append(movedDirs, d)
			} else {
				LogWarning("[快照] 隔离目录 [%s] 失败: %s", src, err)
			}
		}
	}
	LogInfo("[快照] 工作区数据隔离完成 (模块数=%d: [%s])", len(movedDirs), strings.Join(movedDirs, ", "))

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "restore",
		Percent: 45,
		Stage:   "正在解压还原快照数据",
		Message: fmt.Sprintf("数据大小 %s", formatBytes(uint64(meta.SizeBytes))),
	})

	LogInfo("[快照] 开始解压归档包至工作区")
	extractStart := time.Now()
	extractErr := extractTarGz(tarPath, globalPkgVar)
	if extractErr != nil {
		LogError("[快照] 解压归档包失败，执行数据回滚: %s", extractErr)
		for _, d := range movedDirs {
			target := filepath.Join(globalPkgVar, d)
			_ = safeRemoveAll(target)
			_ = os.Rename(filepath.Join(trashDir, d), target)
		}
		_ = safeRemoveAll(trashDir)
		_ = Start()
		return fmt.Errorf("快照解压失败，已回滚历史数据: %w", extractErr)
	}
	LogInfo("[快照] 归档包解压完成 (耗时=%s)", time.Since(extractStart).Round(time.Millisecond))

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "restore",
		Percent: 80,
		Stage:   "正在重新初始化应用配置与环境",
		Message: "载入环境参数",
	})

	go func(trash string) {
		_ = safeRemoveAll(trash)
	}(trashDir)

	LogInfo("[快照] 重新加载应用配置与环境")
	InitConfig()
	InitAppEnv()
	migrateFromGitToNpm()
	go installPnpm()

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "restore",
		Percent: 95,
		Stage:   "正在拉起恢复服务进程",
		Message: "等待服务就绪",
	})

	LogInfo("[快照] 快照 [%s] 数据还原完成 (总耗时=%s)，拉起服务", id, time.Since(restoreStart).Round(time.Millisecond))
	state.SetStatus(StatusStopped, "")
	if err := Start(); err != nil {
		LogWarning("[快照] 还原后服务自启失败: %s", err)
	}

	setSnapshotProgress(SnapshotProgress{
		Active:  true,
		Action:  "restore",
		Percent: 100,
		Stage:   "快照还原完成",
		Message: "服务已重新拉起运行",
	})

	notifySnapshot()
	return nil
}

// DeleteSnapshot 删除指定快照
func DeleteSnapshot(id string) error {
	if state.Status() == StatusSnapshotting {
		return fmt.Errorf("已有快照任务正在执行中，请稍候再操作")
	}

	if !validIDRegex.MatchString(id) {
		return fmt.Errorf("非法快照 ID: %s", id)
	}

	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	snapDir := filepath.Join(globalSnapshotsDir, id)
	if _, err := os.Stat(snapDir); err != nil {
		return fmt.Errorf("快照不存在: %s", id)
	}

	if err := safeRemoveAll(snapDir); err != nil {
		return fmt.Errorf("删除快照失败: %w", err)
	}

	LogInfo("[快照] 已删除快照 [%s]", id)
	notifySnapshot()
	return nil
}
