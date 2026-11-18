package updater

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// maxBinarySize bounds the download so a malicious or mistaken asset cannot
	// fill the data volume.
	maxBinarySize = 64 << 20
	maxSumsSize   = 1 << 20
	maxSigSize    = 4 << 10
	downloadWait  = 3 * time.Minute
)

// Installer downloads and verifies a release binary.
//
// It never starts the new build: staging the file under /data/bin is the whole
// job. Restarting is a separate decision the caller makes after reporting
// success, which keeps a failed restart from looking like a failed download.
type Installer struct {
	Client *http.Client
	// Dir is where the binary is staged; it must be writable.
	Dir string
	// PublicKey overrides the package default when non-empty (used by tests).
	PublicKey string
}

// Install downloads release into Dir, verifies it and returns the path to the
// staged binary. The file is renamed into place only after every check passes,
// so a partial download never becomes /data/bin/obblog.
func (in *Installer) Install(ctx context.Context, release *Release) (string, error) {
	if release.BinaryURL == "" || release.ChecksumsURL == "" || release.SignatureURL == "" {
		return "", errors.New("该版本缺少必需的发布文件")
	}
	key, err := parsePublicKey(in.key())
	if err != nil {
		return "", err
	}
	// Readable by the service account only; the entrypoint runs as that user.
	if err = os.MkdirAll(in.Dir, 0o755); err != nil {
		return "", fmt.Errorf("无法创建更新目录: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, downloadWait)
	defer cancel()

	sums, err := in.fetch(ctx, release.ChecksumsURL, maxSumsSize)
	if err != nil {
		return "", fmt.Errorf("下载校验和失败: %w", err)
	}
	signature, err := in.fetch(ctx, release.SignatureURL, maxSigSize)
	if err != nil {
		return "", fmt.Errorf("下载签名失败: %w", err)
	}
	// Signature comes first: the checksum file is untrusted until it verifies,
	// so there is no point reading values out of it before this passes.
	if !Verify(key, sums, signature) {
		return "", errors.New("校验和签名无效，已拒绝更新")
	}
	want, err := expectedSum(sums, release.BinaryName)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(in.Dir, ".download-")
	if err != nil {
		return "", fmt.Errorf("无法写入更新目录: %w", err)
	}
	tmpName := tmp.Name()
	// The descriptor is closed explicitly below; the deferred close only covers
	// the early-return paths, so it must tolerate an already closed file.
	closed := false
	defer func() {
		if !closed {
			tmp.Close()
		}
	}()
	defer os.Remove(tmpName)

	got, written, err := in.copyWithHash(ctx, release.BinaryURL, tmp)
	if err != nil {
		return "", fmt.Errorf("下载更新包失败: %w", err)
	}
	if written == 0 {
		return "", errors.New("更新包为空")
	}
	if got != want {
		return "", errors.New("更新包校验和不匹配，已拒绝更新")
	}
	if err = tmp.Sync(); err != nil {
		return "", fmt.Errorf("写入磁盘失败: %w", err)
	}
	// Chmod before Close: it acts on the descriptor, which closing invalidates.
	if err = tmp.Chmod(0o755); err != nil {
		return "", fmt.Errorf("设置执行权限失败: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("写入磁盘失败: %w", err)
	}
	closed = true
	if err = verifyExecutable(tmpName); err != nil {
		return "", err
	}

	final := filepath.Join(in.Dir, "obblog")
	if err = os.Rename(tmpName, final); err != nil {
		return "", fmt.Errorf("替换可执行文件失败: %w", err)
	}
	return final, nil
}

func (in *Installer) key() string {
	if in.PublicKey != "" {
		return in.PublicKey
	}
	return PublicKey
}

func (in *Installer) client() *http.Client {
	if in.Client != nil {
		return in.Client
	}
	return &http.Client{Timeout: downloadWait}
}

func (in *Installer) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := in.client().Do(request)
	if err != nil {
		return nil, errors.New("无法连接下载地址")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载返回 %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("读取响应失败")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("响应超过大小限制")
	}
	return data, nil
}

func (in *Installer) copyWithHash(ctx context.Context, url string, dst io.Writer) (string, int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	response, err := in.client().Do(request)
	if err != nil {
		return "", 0, errors.New("无法连接下载地址")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("下载返回 %d", response.StatusCode)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(response.Body, maxBinarySize+1))
	if err != nil {
		return "", written, err
	}
	if written > maxBinarySize {
		return "", written, errors.New("更新包超过大小限制")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

// expectedSum pulls one digest out of a sha256sum-style file.
func expectedSum(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum prefixes binary-mode entries with '*'.
		if strings.TrimPrefix(fields[1], "*") == name {
			digest := strings.ToLower(fields[0])
			if len(digest) != 64 {
				return "", errors.New("校验和格式无效")
			}
			return digest, nil
		}
	}
	return "", fmt.Errorf("校验和文件中没有 %s", name)
}

// verifyExecutable rejects a file the kernel would refuse to run. This catches
// a wrong-architecture asset before the restart, where the failure would
// otherwise look like a crash.
func verifyExecutable(name string) error {
	file, err := elf.Open(name)
	if err != nil {
		return errors.New("更新包不是可执行文件")
	}
	defer file.Close()
	if runtime.GOOS != "linux" {
		return nil
	}
	switch file.Machine {
	case elf.EM_X86_64:
		if runtime.GOARCH != "amd64" {
			return errors.New("更新包架构不匹配")
		}
	case elf.EM_AARCH64:
		if runtime.GOARCH != "arm64" {
			return errors.New("更新包架构不匹配")
		}
	default:
		return errors.New("更新包架构不受支持")
	}
	return nil
}

// installMu serializes installs so two clicks cannot stage the same file.
var installMu sync.Mutex

// LockInstall is exported for callers that need to hold the lock across a
// larger operation, such as staging plus restart.
func LockInstall() { installMu.Lock() }

// UnlockInstall releases the lock taken by LockInstall.
func UnlockInstall() { installMu.Unlock() }
