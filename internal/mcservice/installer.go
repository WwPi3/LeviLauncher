package mcservice

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liteldev/LeviLauncher/internal/apppath"
	"github.com/liteldev/LeviLauncher/internal/eventsink"
	"github.com/liteldev/LeviLauncher/internal/extractor"
	"github.com/liteldev/LeviLauncher/internal/leviloader"
	"github.com/liteldev/LeviLauncher/internal/msixvc"
	"github.com/liteldev/LeviLauncher/internal/types"
	"github.com/liteldev/LeviLauncher/internal/utils"
	"github.com/liteldev/LeviLauncher/internal/versions"
	"gopkg.in/natefinch/npipe.v2"
)
type VersionStatus struct {
	Version      string `json:"version"`
	IsInstalled  bool   `json:"isInstalled"`
	IsDownloaded bool   `json:"isDownloaded"`
	Type         string `json:"type"`
	PackageType  string `json:"packageType"`
}

func StartMsixvcDownload(ctx context.Context, url string, md5sum string) (string, error) {
	return msixvc.StartDownload(ctx, url, md5sum)
}
func ResumeMsixvcDownload()                { msixvc.Resume() }
func CancelMsixvcDownload()                { msixvc.Cancel() }
func CancelMsixvcDownloadTask(dest string) { msixvc.CancelTask(dest) }

func InstallExtractMsixvc(ctx context.Context, name string, folderName string, isPreview bool) string {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "ERR_CANCELED"
	}
	n := strings.TrimSpace(name)
	if n == "" {
		return "ERR_MSIXVC_NOT_SPECIFIED"
	}
	inPath := n
	if !filepath.IsAbs(inPath) {
		if dir, err := apppath.InstallersDir(); err == nil && dir != "" {
			inPath += ".msixvc"
			inPath = filepath.Join(dir, inPath)
		}
	}
	if !utils.FileExists(inPath) {
		return "ERR_MSIXVC_NOT_FOUND"
	}
	vdir, err := apppath.InstancesDir()
	if err != nil || strings.TrimSpace(vdir) == "" {
		return "ERR_ACCESS_VERSIONS_DIR"
	}
	folder := strings.TrimSpace(folderName)
	if folder == "" || folder == "." || folder == ".." || filepath.Base(folder) != folder || strings.ContainsAny(folder, "<>:\"/\\|?*") {
		return "ERR_INVALID_FOLDER_NAME"
	}
	outDir := filepath.Join(vdir, folder)
	if _, err := os.Lstat(outDir); err == nil {
		return "ERR_TARGET_EXISTS"
	} else if !os.IsNotExist(err) {
		return "ERR_ACCESS_VERSIONS_DIR"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "ERR_CREATE_TARGET_DIR"
	}

	pipeName := fmt.Sprintf("\\\\.\\pipe\\levi_msixvc_progress_%d", time.Now().UnixNano())
	ln, err := npipe.Listen(pipeName)
	if err != nil {
		_ = os.RemoveAll(outDir)
		eventsink.From(ctx).Emit(EventExtractError, "ERR_CREATE_PIPE")
		return "ERR_CREATE_PIPE"
	}
	defer ln.Close()

	pipeDone := make(chan struct{})
	go func() {
		defer close(pipeDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			line := strings.ReplaceAll(scanner.Text(), "\\", "/")
			var p struct {
				File          string "json:\"file\""
				Current       int64  "json:\"current\""
				Total         int64  "json:\"total\""
				GlobalCurrent int64  "json:\"global_current\""
				GlobalTotal   int64  "json:\"global_total\""
			}
			if err := json.Unmarshal([]byte(line), &p); err == nil {
				eventsink.From(ctx).Emit(EventExtractProgress, types.ExtractProgress{
					Dir: outDir, Bytes: p.Current, TotalBytes: p.Total,
					GlobalCurrent: p.GlobalCurrent, GlobalTotal: p.GlobalTotal,
					CurrentFile: p.File, Ts: time.Now().UnixMilli(),
				})
			}
		}
	}()

	rc, msg := extractor.GetWithPipe(inPath, outDir, pipeName)
	_ = ln.Close()
	<-pipeDone
	if rc != 0 {
		if strings.TrimSpace(msg) == "" {
			msg = "ERR_APPX_INSTALL_FAILED"
		}
		eventsink.From(ctx).Emit(EventExtractError, msg)
		_ = os.RemoveAll(outDir)
		return msg
	}
	// The legacy DLL has no cancellation API. Do not keep a partially useful
	// version if cancellation was requested while its synchronous call ran.
	if err := ctx.Err(); err != nil {
		_ = os.RemoveAll(outDir)
		eventsink.From(ctx).Emit(EventExtractError, "ERR_CANCELED")
		return "ERR_CANCELED"
	}
	if err := leviloader.EnsureForVersion(ctx, outDir); err != nil {
		_ = os.RemoveAll(outDir)
		msg = "ERR_LEVILOADER_PREPARE: " + err.Error()
		eventsink.From(ctx).Emit(EventExtractError, msg)
		return msg
	}
	eventsink.From(ctx).Emit(EventExtractDone, outDir)
	return ""
}
func ResolveDownloadedMsixvc(version string, versionType string) string {
	dir, err := apppath.InstallersDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".msixvc") {
			continue
		}
		ext := filepath.Ext(name)
		if strings.ToLower(ext) == ".msixvc" {
			name = name[:len(name)-len(ext)]
		} else {
			name = strings.TrimSuffix(name, ".msixvc")
		}
		b := strings.TrimSpace(name)
		v := strings.TrimSpace(version)
		bl := strings.ToLower(b)
		vl := strings.ToLower(v)
		channel := strings.ToLower(strings.TrimSpace(versionType))
		if vl == bl || ((channel == "release" || channel == "preview") && (bl == channel+" "+vl || bl == "minecraft-"+channel+"-"+vl)) {
			return name
		}
	}
	return ""
}

func DeleteDownloadedMsixvc(version string, versionType string) string {
	name := strings.TrimSpace(ResolveDownloadedMsixvc(version, versionType))
	if name == "" {
		return "ERR_MSIXVC_NOT_FOUND"
	}
	dir, err := apppath.InstallersDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return "ERR_ACCESS_INSTALLERS_DIR"
	}
	path := filepath.Join(dir, name+".msixvc")
	if !utils.FileExists(path) {
		alt := filepath.Join(dir, name)
		if utils.FileExists(alt) {
			path = alt
		}
	}
	if !utils.FileExists(path) {
		return "ERR_MSIXVC_NOT_FOUND"
	}
	if err := os.Remove(path); err != nil {
		return "ERR_WRITE_TARGET"
	}
	return ""
}

func GetInstallerDir() string {
	dir, err := apppath.InstallersDir()
	if err != nil {
		return ""
	}
	return dir
}

func GetVersionsDir() string {
	dir, err := apppath.InstancesDir()
	if err != nil {
		return ""
	}
	return dir
}

func GetVersionStatus(version string, versionType string) VersionStatus {
	return GetVersionStatusForPackage(version, versionType, "gdk")
}

func GetVersionStatusForPackage(version, channel, packageType string) VersionStatus {
	return versionStatusForPackage(version, channel, packageType, ListVersionMetas())
}

func versionStatusForPackage(version, channel, packageType string, metas []versions.VersionMeta) VersionStatus {
	platform := versions.NormalizePackageType(packageType)
	status := VersionStatus{Version: version, Type: channel, PackageType: platform}
	if platform == "uwp" {
		status.IsDownloaded = ResolveDownloadedUWP(version, strings.ToLower(channel)) != ""
	} else {
		status.IsDownloaded = ResolveDownloadedMsixvc(version, channel) != ""
	}
	for _, meta := range metas {
		if meta.GameVersion == version && strings.EqualFold(meta.Type, channel) && versions.NormalizePackageType(meta.PackageType) == platform {
			status.IsInstalled = true
			break
		}
	}
	return status
}

func GetAllVersionsStatus(versionsList []map[string]interface{}) []VersionStatus {
	var results []VersionStatus
	metas := ListVersionMetas()
	for _, versionData := range versionsList {
		version, ok := versionData["version"].(string)
		if !ok {
			version, ok = versionData["short"].(string)
			if !ok {
				continue
			}
		}
		versionType, ok := versionData["type"].(string)
		if !ok {
			versionType = "release"
		}
		packageType, _ := versionData["packageType"].(string)
		status := versionStatusForPackage(version, versionType, packageType, metas)
		results = append(results, status)
	}
	return results
}
