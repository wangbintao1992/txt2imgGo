package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	aeszip "github.com/yeka/zip"
)

type archiveFormat string

const (
	formatZIP archiveFormat = "zip"
	format7z  archiveFormat = "7z"
)

var archiveFormatLabels = []string{"ZIP（内置 AES-256）", "7z（需安装 7-Zip）"}

func (a *app) currentArchiveFormat() archiveFormat {
	if a.archiveFormat == archiveFormatLabels[1] {
		return format7z
	}
	return formatZIP
}

func archiveExtension(f archiveFormat) string {
	if f == format7z {
		return ".7z"
	}
	return ".zip"
}

// find7z 查找系统上的 7-Zip 命令行。7z 的 AES-256 加密没有可靠的纯 Go 写入实现，
// 因此 7z 导出依赖外部命令；ZIP 则使用内置实现，随时可用。
func find7z() string {
	for _, name := range []string{"7zz", "7z", "7za"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	var candidates []string
	if runtime.GOOS == "windows" {
		candidates = []string{
			`C:\Program Files\7-Zip\7z.exe`,
			`C:\Program Files (x86)\7-Zip\7z.exe`,
		}
	} else {
		candidates = []string{
			"/opt/homebrew/bin/7zz",
			"/opt/homebrew/bin/7z",
			"/usr/local/bin/7zz",
			"/usr/local/bin/7z",
			"/usr/bin/7zz",
			"/usr/bin/7z",
			"/usr/bin/7za",
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// writeZIPArchive 用内置 AES-256 写 ZIP；password 为空时不加密。
func writeZIPArchive(path string, files []string, password string, progress func(done, total int)) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := aeszip.NewWriter(f)
	for i, name := range files {
		base := filepath.Base(name)
		var w io.Writer
		if password != "" {
			w, err = zw.Encrypt(base, password, aeszip.AES256Encryption)
		} else {
			w, err = zw.Create(base)
		}
		if err != nil {
			_ = f.Close()
			return err
		}
		src, err := os.Open(name)
		if err != nil {
			_ = f.Close()
			return err
		}
		_, copyErr := io.Copy(w, src)
		closeErr := src.Close()
		if copyErr != nil {
			_ = f.Close()
			return copyErr
		}
		if closeErr != nil {
			_ = f.Close()
			return closeErr
		}
		if progress != nil {
			progress(i+1, len(files))
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// write7zArchive 调用系统 7-Zip 创建 .7z；设置密码时同时加密文件头。
func write7zArchive(bin, path string, files []string, password string, progress func(done, total int)) error {
	if len(files) == 0 {
		return fmt.Errorf("没有可压缩的文件")
	}
	dir := filepath.Dir(files[0])
	args := []string{"a", "-t7z", "-y", "-bd"}
	if password != "" {
		args = append(args, "-p"+password, "-mhe=on")
	}
	args = append(args, path, ".")
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("7-Zip 压缩失败: %w", err)
		}
		return fmt.Errorf("7-Zip 压缩失败: %v: %s", err, msg)
	}
	if progress != nil {
		progress(len(files), len(files))
	}
	return nil
}
