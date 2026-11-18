package web

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"obblog/internal/updater"
	"obblog/internal/version"
)

// updateInstall downloads, verifies and stages a release. Restarting is a
// separate call so a failed restart does not read as a failed download.
//
// Installing is always explicit: the auto-install preference only decides
// whether the panel offers the button prominently, never whether this runs on
// its own.
func (a *App) updateInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	if !a.updateInstallEnabled() {
		fail(w, http.StatusConflict, "当前部署不支持自动更新")
		return
	}

	a.installMu.Lock()
	if a.installing {
		a.installMu.Unlock()
		fail(w, http.StatusConflict, "更新正在进行中")
		return
	}
	a.installing = true
	a.installMu.Unlock()
	defer func() {
		a.installMu.Lock()
		a.installing = false
		a.installMu.Unlock()
	}()

	release, err := a.updates.Latest(r.Context())
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if !version.Newer(release.Tag, version.Version) {
		fail(w, http.StatusConflict, "当前已是最新版本")
		return
	}

	installer := &updater.Installer{Dir: a.updateDir()}
	path, err := installer.Install(r.Context(), release)
	if err != nil {
		log.Printf("update install: %v", err)
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("update staged: %s at %s", release.Tag, path)
	respond(w, map[string]any{"ok": true, "version": release.Tag, "path": path, "restartRequired": true})
}

// updateDir is where a staged binary lives. The container entrypoint prefers
// this path, so a restart picks up the new build without a new image.
// updateDir is where a staged binary lives. It follows the data directory so
// the file lands on the same volume that survives a container rebuild; a fixed
// /data path would silently write to the container layer when -data points
// somewhere else.
func (a *App) updateDir() string {
	if dir := os.Getenv("OBBLOG_UPDATE_DIR"); dir != "" {
		return dir
	}
	if a.dataDir != "" {
		return filepath.Join(a.dataDir, "bin")
	}
	return "/data/bin"
}

// updateInstallEnabled reports whether this process may replace itself. Image
// builds without a signing key, and the disposable test runtime, must not.
func (a *App) updateInstallEnabled() bool {
	if a.testMode || a.publications == nil {
		return false
	}
	return updater.PublicKey != ""
}

// updateRestart replaces the current process with the staged binary.
//
// syscall.Exec keeps the PID, so the container is not recreated and the health
// check keeps reporting. The caller must have already staged and verified the
// file; this only exchanges the process image.
func (a *App) updateRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	path := updateBinaryPath(a.updateDir())
	if path == "" {
		fail(w, http.StatusConflict, "没有待启动的更新包")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法定位当前程序")
		return
	}
	args := os.Args
	// A process started through a launcher keeps that argv[0]; replacing the
	// image with the staged path avoids reopening the old binary.
	if len(args) > 0 {
		args[0] = path
	} else {
		args = []string{path}
	}
	env := os.Environ()

	respond(w, map[string]any{"ok": true, "restarting": true})
	// Flush before exec: the response must reach the browser, because this
	// process is about to stop existing.
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	go func() {
		time.Sleep(time.Second)
		log.Printf("restarting into %s (from %s)", path, exe)
		if err := syscall.Exec(path, args, env); err != nil {
			log.Printf("exec %s: %v", path, err)
		}
	}()
}

// updateBinaryPath returns the staged binary if it looks runnable.
func updateBinaryPath(dir string) string {
	path := dir + "/obblog"
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return ""
	}
	return path
}
