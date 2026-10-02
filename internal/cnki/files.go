package cnki

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var reservedName = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)`)

func safeName(name string) error {
	if name == "" || len([]rune(name)) > 180 || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.ContainsAny(name, `/\<>:"|?*`) || reservedName.MatchString(name) {
		return fail("INVALID_FILENAME", "文件名无效，不能包含目录或系统保留名")
	}
	for _, r := range name {
		if r < 32 {
			return fail("INVALID_FILENAME", "文件名不能包含控制字符")
		}
	}
	return nil
}
func outputRoot(root, folder string) (*os.Root, error) {
	parent, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err = parent.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	info, err := parent.Lstat(folder)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fail("UNSAFE_OUTPUT_PATH", "输出目录不能是符号链接")
	}
	return parent.OpenRoot(folder)
}
func writeArtifact(root, folder, name string, reader io.Reader) (string, int64, string, error) {
	if err := safeName(name); err != nil {
		return "", 0, "", err
	}
	dir, err := outputRoot(root, folder)
	if err != nil {
		return "", 0, "", err
	}
	defer dir.Close()
	if _, e := dir.Lstat(name); e == nil {
		return "", 0, "", fail("FILE_EXISTS", "文件已存在，未覆盖")
	}
	temp := "." + newID("write_")
	file, err := dir.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", 0, "", err
	}
	defer dir.Remove(temp)
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), reader)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, "", err
	}
	if err = dir.Link(temp, name); err != nil {
		return "", 0, "", fail("FILE_EXISTS", "目标已存在或无法原子写入；未覆盖")
	}
	return filepath.Join(root, folder, name), size, hex.EncodeToString(hash.Sum(nil)), nil
}
func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	_, err = io.Copy(h, file)
	return hex.EncodeToString(h.Sum(nil)), err
}
