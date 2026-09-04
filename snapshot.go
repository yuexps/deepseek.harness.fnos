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
)

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

// SystemResourceStatus 统一系统资源状态
type SystemResourceStatus struct {
	Disk DiskUsage `json:"disk"`
	Mem  MemInfo   `json:"mem"`
	CPU  CPUInfo   `json:"cpu"`
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

// GetSystemResourceStatus 获取系统资源指标快照
func GetSystemResourceStatus() SystemResourceStatus {
	disk, _ := getDiskUsage(globalPkgVar)
	mem, _ := getMemoryInfo()
	cpu, _ := getCPUInfo()
	return SystemResourceStatus{
		Disk: disk,
		Mem:  mem,
		CPU:  cpu,
	}
}

const (
	// MinDiskFreeBytes 磁盘至少 10GB 可用
	MinDiskFreeBytes uint64 = 10 * 1024 * 1024 * 1024
	// MinMemAvailableBytes 内存至少 1.5GB 可用
	MinMemAvailableBytes uint64 = 1536 * 1024 * 1024
	// MinCPUCores CPU 至少 2 核
	MinCPUCores = 2
	// MaxCPULoadRatio CPU 负载上限比率 (Load1 / Cores)
	MaxCPULoadRatio = 1.5
)

// checkHardwareBaseline 检查硬件基线：硬盘>=10G可用，内存>=1.5G可用，CPU>=2核且不过载
func checkHardwareBaseline(extraDisk uint64) error {
	requiredDisk := MinDiskFreeBytes
	if extraDisk > requiredDisk {
		requiredDisk = extraDisk
	}

	disk, err := getDiskUsage(globalPkgVar)
	if err == nil && disk.FreeBytes < requiredDisk {
		return fmt.Errorf("磁盘可用空间不足 (当前: %s, 要求: >= %s)，请先清理硬盘空间", formatBytes(disk.FreeBytes), formatBytes(requiredDisk))
	}

	mem, err := getMemoryInfo()
	if err == nil && mem.AvailableBytes < MinMemAvailableBytes {
		return fmt.Errorf("系统可用内存不足 (当前: %s, 要求: >= 1.5 GB)，避免构建或打包被系统 OOM 强杀", formatBytes(mem.AvailableBytes))
	}

	cpu, err := getCPUInfo()
	if err == nil {
		if cpu.Cores < MinCPUCores {
			return fmt.Errorf("CPU 核心数不足 (当前: %d 核, 要求: >= 2 核)，不满足运行要求", cpu.Cores)
		}
		if cpu.Load1 > float64(cpu.Cores)*MaxCPULoadRatio {
			return fmt.Errorf("系统 CPU 当前处于高负载繁忙状态 (1分钟负载: %.2f, %d 核)，请稍后重试", cpu.Load1, cpu.Cores)
		}
	}

	return nil
}

// CheckResourceForBuild 源码构建前资源检查
func CheckResourceForBuild() error {
	return checkHardwareBaseline(0)
}

// CheckResourceForSnapshot 创建快照前资源检查
func CheckResourceForSnapshot() error {
	return checkHardwareBaseline(0)
}

// CheckResourceForRestore 还原快照前资源检查
func CheckResourceForRestore(snapshotSizeBytes int64) error {
	requiredDisk := uint64(snapshotSizeBytes)*2 + 500*1024*1024
	return checkHardwareBaseline(requiredDisk)
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
	GitCommit        string `json:"git_commit"`
	HarnessVersion   string `json:"harness_version"`
	VersionTag       string `json:"version_tag"`
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
	Items          []SnapshotMeta `json:"items"`
	TotalSizeBytes int64          `json:"total_size_bytes"`
	DiskFreeBytes  uint64         `json:"disk_free_bytes"`
	DiskTotalBytes uint64         `json:"disk_total_bytes"`
}

func snapshotsBaseDir() string {
	return filepath.Join(globalPkgVar, "snapshots")
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ListSnapshots 获取所有快照列表及统计
func ListSnapshots() (SnapshotSummary, error) {
	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	base := snapshotsBaseDir()
	_ = os.MkdirAll(base, 0755)

	entries, err := os.ReadDir(base)
	if err != nil {
		return SnapshotSummary{Items: []SnapshotMeta{}}, err
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
	}, nil
}

// CreateSnapshot 创建新快照
func CreateSnapshot(params CreateSnapshotParams) (*SnapshotMeta, error) {
	cur := state.Status()
	if cur == StatusBuilding {
		return nil, fmt.Errorf("服务正在源码构建中，请稍候再试")
	}
	if cur == StatusSnapshotting {
		return nil, fmt.Errorf("已有快照任务正在执行中，请勿重复操作")
	}
	if cur == StatusStarting {
		return nil, fmt.Errorf("服务正在启动中，请等待启动完成后再创建快照")
	}

	if strings.TrimSpace(params.Name) == "" {
		params.Name = "手动快照_" + time.Now().Format("0102_1504")
	}

	if err := CheckResourceForSnapshot(); err != nil {
		return nil, err
	}

	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	// 记录服务运行状态，确保冷备数据 100% 纯净与落盘
	wasRunning := (state.Status() == StatusRunning)
	if wasRunning {
		LogInfo("正在安全暂停服务，确保数据彻底落盘以创建纯净冷备快照...")
		state.SetStatus(StatusSnapshotting, "正在安全暂停服务准备创建快照...")
		KillHarness()
		time.Sleep(1 * time.Second)
	}

	id := fmt.Sprintf("snap_%s_%s", time.Now().Format("20060102_150405"), randHex(3))
	snapDir := filepath.Join(snapshotsBaseDir(), id)
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		if wasRunning {
			state.SetStatus(StatusStopped, "")
			_ = Start()
		}
		return nil, fmt.Errorf("创建快照目录失败: %w", err)
	}

	tarPath := filepath.Join(snapDir, "data.tar.gz")

	pluginCount := 0
	if deps, _, _, err := readProfileManifest(); err == nil {
		pluginCount = len(deps)
	}

	harnessVer := readVersion()
	gitCommit := gitHead()

	level := params.CompressionLevel
	if level < 1 || level > 9 {
		level = gzip.BestSpeed
	}

	meta := SnapshotMeta{
		ID:               id,
		Name:             strings.TrimSpace(params.Name),
		CreatedAt:        time.Now().Unix(),
		AppVersion:       globalAppVer,
		GitCommit:        gitCommit,
		HarnessVersion:   harnessVer,
		VersionTag:       formatVersionTag(harnessVer, gitCommit),
		PluginCount:      pluginCount,
		CompressionLevel: level,
	}

	LogInfo("开始全量打包快照 [%s] (压缩级别: Lv%d)...", id, level)
	if err := archiveSnapshotData(tarPath, level); err != nil {
		_ = os.RemoveAll(snapDir)
		if wasRunning {
			state.SetStatus(StatusStopped, "")
			_ = Start()
		}
		return nil, fmt.Errorf("快照打包失败: %w", err)
	}

	fi, err := os.Stat(tarPath)
	if err == nil {
		meta.SizeBytes = fi.Size()
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(filepath.Join(snapDir, "meta.json"), metaBytes, 0644)

	// 打包完成后，若之前为运行中则自动拉起恢复服务
	if wasRunning {
		LogInfo("快照打包完成，正在自动拉起恢复服务...")
		state.SetStatus(StatusStopped, "")
		if err := Start(); err != nil {
			LogWarning("快照创建后服务自启失败: %s", err)
		}
	}

	LogInfo("快照 [%s] 创建成功 (大小: %s)", id, formatBytes(uint64(meta.SizeBytes)))
	notifySnapshot()
	return &meta, nil
}

// archiveSnapshotData 将 src, home, dsh-data, config.json 全量打包至 tar.gz
func archiveSnapshotData(tarPath string, level int) error {
	f, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if level < 1 || level > 9 {
		level = gzip.BestSpeed
	}
	gw, err := gzip.NewWriterLevel(f, level)
	if err != nil {
		return err
	}
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	targets := []string{"config.json", "src", "home", "dsh-data"}

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
			if err := addFileToTar(tw, globalPkgVar, rel, fi); err != nil {
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

			return addFileToTar(tw, globalPkgVar, relPath, info)
		})

		if err != nil {
			return err
		}
	}

	return nil
}

func addFileToTar(tw *tar.Writer, baseDir, relPath string, info os.FileInfo) error {
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

	_, err = io.Copy(tw, file)
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
		return fmt.Errorf("gzip 头损坏: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	hasFiles := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar 结构异常: %w", err)
		}
		if hdr.Name != "" {
			hasFiles = true
		}
	}
	if !hasFiles {
		return fmt.Errorf("快照包内无有效文件")
	}
	return nil
}

// RestoreSnapshot 一键干净还原快照（原子重命名容灾）
func RestoreSnapshot(id string) error {
	cur := state.Status()
	if cur == StatusBuilding {
		return fmt.Errorf("服务正在源码构建中，无法还原快照，请稍候再试")
	}
	if cur == StatusSnapshotting {
		return fmt.Errorf("已有快照任务正在执行中，请勿重复操作")
	}

	if !validIDRegex.MatchString(id) {
		return fmt.Errorf("非法快照 ID: %s", id)
	}

	snapDir := filepath.Join(snapshotsBaseDir(), id)
	metaFile := filepath.Join(snapDir, "meta.json")
	metaData, err := os.ReadFile(metaFile)
	if err != nil {
		return fmt.Errorf("快照元数据不存在: %w", err)
	}

	var meta SnapshotMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("解析快照元数据失败: %w", err)
	}

	tarPath := filepath.Join(snapDir, "data.tar.gz")
	if _, err := os.Stat(tarPath); err != nil {
		return fmt.Errorf("快照压缩包缺失: %w", err)
	}

	if err := CheckResourceForRestore(meta.SizeBytes); err != nil {
		return err
	}

	if err := verifySnapshotArchive(tarPath); err != nil {
		return fmt.Errorf("快照校验失败，取消还原: %w", err)
	}

	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	LogInfo("开始还原快照 [%s] (%s)...", id, meta.Name)

	state.SetStatus(StatusSnapshotting, "正在停止服务准备还原快照...")
	KillHarness()
	time.Sleep(1 * time.Second)

	trashDir := filepath.Join(globalPkgVar, fmt.Sprintf("_trash_restore_%s", time.Now().Format("20060102_150405")))
	_ = os.MkdirAll(trashDir, 0755)

	targetDirs := []string{"config.json", "src", "home", "dsh-data"}
	var movedDirs []string

	for _, d := range targetDirs {
		src := filepath.Join(globalPkgVar, d)
		dst := filepath.Join(trashDir, d)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err == nil {
				movedDirs = append(movedDirs, d)
			} else {
				LogWarning("重命名隔离目录 [%s] 失败: %s", src, err)
			}
		}
	}

	extractErr := extractTarGz(tarPath, globalPkgVar)
	if extractErr != nil {
		LogWarning("快照解压失败，立即执行回滚自愈: %s", extractErr)
		for _, d := range movedDirs {
			target := filepath.Join(globalPkgVar, d)
			_ = safeRemoveAll(target)
			_ = os.Rename(filepath.Join(trashDir, d), target)
		}
		_ = safeRemoveAll(trashDir)
		_ = Start()
		return fmt.Errorf("快照解压失败，已自动回滚当前状态: %w", extractErr)
	}

	go func(trash string) {
		_ = safeRemoveAll(trash)
	}(trashDir)

	InitConfig(globalPkgVar)
	InitAppEnv(globalPkgVar)

	LogInfo("快照 [%s] 数据恢复完成，正在拉起服务", id)
	state.SetStatus(StatusStopped, "")
	if err := Start(); err != nil {
		LogWarning("还原后服务自启失败: %s", err)
	}

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

	snapDir := filepath.Join(snapshotsBaseDir(), id)
	if _, err := os.Stat(snapDir); err != nil {
		return fmt.Errorf("快照不存在: %s", id)
	}

	if err := safeRemoveAll(snapDir); err != nil {
		return fmt.Errorf("删除快照失败: %w", err)
	}

	LogInfo("已删除快照 [%s]", id)
	notifySnapshot()
	return nil
}
