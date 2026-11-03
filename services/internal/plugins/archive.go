package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const (
	maxArchiveBytes         = 5 << 20
	maxArchiveExpandedBytes = 20 << 20
	maxArchiveEntries       = 1000
)

// ResolveArchive reads package metadata only. Archive files never reach disk or
// an interpreter, and package scripts and declared dependencies are not loaded.
func ResolveArchive(ctx context.Context, filename string, content []byte) (Plugin, error) {
	if err := ctx.Err(); err != nil {
		return Plugin{}, err
	}
	filename = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	lower := strings.ToLower(filename)
	if filename == "." || len(filename) > 255 || !utf8.ValidString(filename) || strings.IndexFunc(filename, unicode.IsControl) >= 0 || strings.Contains(filename, ":") || !(strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")) {
		return Plugin{}, invalid("请选择有效的 .tgz 或 .tar.gz npm 插件包")
	}
	if len(content) > maxArchiveBytes {
		return Plugin{}, archiveTooLarge("插件包压缩文件不能超过 5 MiB")
	}
	compressed := bytes.NewReader(content)
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return Plugin{}, invalid("插件包 gzip 格式损坏或不完整")
	}
	reader.Multistream(false)
	expanded, err := io.ReadAll(io.LimitReader(reader, maxArchiveExpandedBytes+1))
	_ = reader.Close()
	if len(expanded) > maxArchiveExpandedBytes {
		return Plugin{}, archiveTooLarge("插件包展开后不能超过 20 MiB")
	}
	if err != nil || compressed.Len() != 0 {
		return Plugin{}, invalid("插件包 gzip 格式损坏或含有额外内容")
	}
	if err = ctx.Err(); err != nil {
		return Plugin{}, err
	}
	stream := bytes.NewReader(expanded)
	archive := tar.NewReader(stream)
	files := make(map[string]bool)
	var manifestJSON []byte
	count, lastEnd := 0, 0
	for {
		if err = ctx.Err(); err != nil {
			return Plugin{}, err
		}
		header, nextErr := archive.Next()
		if nextErr == io.EOF {
			// Require a complete tar terminator; reject hidden data after it.
			trailer := (lastEnd + 511) / 512 * 512
			if len(expanded)-trailer < 1024 || len(expanded)%512 != 0 || len(bytes.Trim(expanded[trailer:], "\x00")) != 0 {
				return Plugin{}, invalid("插件包 tar 格式损坏或含有额外内容")
			}
			break
		}
		if nextErr != nil {
			return Plugin{}, invalid("插件包 tar 格式损坏或不完整")
		}
		count++
		if count > maxArchiveEntries {
			return Plugin{}, archiveTooLarge("插件包不能超过 1000 个文件或目录")
		}
		name, ok := archivePath(header.Name)
		if !ok {
			return Plugin{}, invalid("插件包包含不安全的文件路径")
		}
		if _, exists := files[name]; exists {
			return Plugin{}, invalid("插件包包含重复的文件路径")
		}
		regular := header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA
		if !regular && header.Typeflag != tar.TypeDir {
			return Plugin{}, invalid("插件包只能包含普通文件和目录")
		}
		for key := range header.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse.") {
				return Plugin{}, invalid("插件包不能包含稀疏文件")
			}
		}
		files[name] = regular
		if name == "package/package.json" {
			if !regular {
				return Plugin{}, invalid("插件包缺少有效的 package/package.json")
			}
			if header.Size > registryLimit {
				return Plugin{}, archiveTooLarge("插件包 package.json 不能超过 1 MiB")
			}
			manifestJSON, err = io.ReadAll(archive)
		} else {
			_, err = io.Copy(io.Discard, archive)
		}
		if err != nil {
			return Plugin{}, invalid("插件包文件内容损坏或不完整")
		}
		lastEnd = len(expanded) - stream.Len()
	}
	if len(manifestJSON) == 0 {
		return Plugin{}, invalid("插件包缺少 package/package.json")
	}
	var manifest registryManifest
	if json.Unmarshal(manifestJSON, &manifest) != nil {
		return Plugin{}, invalid("插件包 package.json 格式不正确")
	}
	plugin, patch, err := pluginFromManifest(manifest)
	if err != nil {
		return Plugin{}, err
	}
	if !files[path.Join("package", patch)] {
		return Plugin{}, invalid("插件包缺少 dsh.bundle.patch 声明的普通文件")
	}
	digest := sha256.Sum256(content)
	plugin.AdditionMethod, plugin.Filename, plugin.SHA256 = "offline", filename, hex.EncodeToString(digest[:])
	plugin.SourceNote = "上传包信息及 DSH bundle 声明"
	return plugin, nil
}

func archivePath(name string) (string, bool) {
	if name == "" || len(name) > 4096 || !utf8.ValidString(name) || path.IsAbs(name) || strings.ContainsAny(name, "\\:") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", false
		}
	}
	name = path.Clean(name)
	return name, name == "package" || strings.HasPrefix(name, "package/")
}

func archiveTooLarge(message string) error {
	return webapi.Fail(413, "PLUGIN_TOO_LARGE", message)
}
