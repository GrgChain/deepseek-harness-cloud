package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type pluginArchiveEntry struct {
	name string
	body []byte
	kind byte
	link string
}

func pluginTar(t *testing.T, entries []pluginArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Typeflag: kind, Mode: 0644, Size: int64(len(entry.body)), Linkname: entry.link}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func pluginGzip(t *testing.T, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func pluginArchiveEntries(t *testing.T, manifest any) []pluginArchiveEntry {
	t.Helper()
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return []pluginArchiveEntry{{name: "package/package.json", body: body},
		{name: "package/cordis.patch.yml", body: []byte("plugins: {}\n")}}
}

func TestResolveArchiveMatchesOnlineMetadataWithoutNetworkOrExecution(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = registryRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("offline archive attempted an HTTP request")
		return nil, errors.New("unexpected request")
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	manifest := manifestFixture()
	manifest["scripts"] = map[string]string{"preinstall": "must-never-run", "postinstall": "must-never-run"}
	entries := append([]pluginArchiveEntry{{name: "package/", kind: tar.TypeDir}}, pluginArchiveEntries(t, manifest)...)
	entries = append(entries, pluginArchiveEntry{name: "package/install.js", body: []byte("throw Error('must never execute')")})
	content := pluginGzip(t, pluginTar(t, entries))
	registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
		return manifestResponse(t, request, 200, manifest), nil
	})}}
	online, err := registry.Resolve(context.Background(), "@community/context-memory")
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{"plugin.tgz", "../uploads/plugin.tar.gz", `C:\fakepath\plugin.TGZ`} {
		t.Run(filename, func(t *testing.T) {
			offline, err := ResolveArchive(context.Background(), filename, content)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(content)
			if offline.AdditionMethod != "offline" || offline.SHA256 != hex.EncodeToString(digest[:]) || strings.ContainsAny(offline.Filename, "/\\") || offline.SourceNote != "上传包信息及 DSH bundle 声明" || online.SourceNote != "包信息及 DSH bundle 声明" {
				t.Fatalf("invalid archive provenance: %+v", offline)
			}
			if filename != "plugin.tgz" && offline.Filename != "plugin.tar.gz" && offline.Filename != "plugin.TGZ" {
				t.Fatalf("archive filename was not reduced to its basename: %q", offline.Filename)
			}
			offline.AdditionMethod, offline.Filename, offline.SHA256 = online.AdditionMethod, "", ""
			offline.SourceNote = online.SourceNote
			if !reflect.DeepEqual(offline, online) {
				t.Fatalf("offline metadata differs from online metadata:\n%+v\n%+v", offline, online)
			}
		})
	}
}

func TestResolveArchiveRejectsInvalidFilenameAndCanceledContext(t *testing.T) {
	content := pluginGzip(t, pluginTar(t, pluginArchiveEntries(t, manifestFixture())))
	for _, filename := range []string{"", ".", "plugin.zip", "plugin.tgz.exe", "plugin\n.tgz", "C:plugin.tgz", strings.Repeat("x", 252) + ".tgz"} {
		t.Run(filename, func(t *testing.T) {
			_, err := ResolveArchive(context.Background(), filename, content)
			registryProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveArchive(ctx, "plugin.tgz", content); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled archive parsing returned %v", err)
	}
}

func TestResolveArchiveRejectsDamagedGzipTarAndHiddenTrailingData(t *testing.T) {
	raw := pluginTar(t, pluginArchiveEntries(t, manifestFixture()))
	valid := pluginGzip(t, raw)
	badChecksum := bytes.Clone(valid)
	badChecksum[len(badChecksum)-8] ^= 1
	cases := map[string][]byte{
		"empty":                    nil,
		"not gzip":                 []byte("not an archive"),
		"truncated gzip":           valid[:len(valid)-1],
		"wrong gzip checksum":      badChecksum,
		"not tar":                  pluginGzip(t, []byte("not tar")),
		"truncated tar file":       pluginGzip(t, raw[:530]),
		"missing tar terminator":   pluginGzip(t, raw[:len(raw)-1024]),
		"extra data after tar":     pluginGzip(t, append(bytes.Clone(raw), bytes.Repeat([]byte{'x'}, 512)...)),
		"extra gzip member":        append(bytes.Clone(valid), valid...),
		"extra data after gzip":    append(bytes.Clone(valid), []byte("extra")...),
		"empty tar lacks manifest": pluginGzip(t, pluginTar(t, nil)),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveArchive(context.Background(), "plugin.tgz", content)
			registryProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
}

func TestResolveArchiveRejectsUnsafePathsAndSpecialEntries(t *testing.T) {
	for _, name := range []string{"/package/outside", "../outside", "package/../outside", "package/nested/../../outside", `package\outside`, "C:/outside", "package/control\nfile", "outside/file", "."} {
		t.Run(name, func(t *testing.T) {
			entries := append(pluginArchiveEntries(t, manifestFixture()), pluginArchiveEntry{name: name})
			_, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, entries)))
			registryProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo} {
		t.Run(string(kind), func(t *testing.T) {
			entries := append(pluginArchiveEntries(t, manifestFixture()), pluginArchiveEntry{name: "package/special", kind: kind, link: "../../outside"})
			_, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, entries)))
			registryProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
	for _, name := range []string{"package/package.json", "./package/package.json", "package/cordis.patch.yml"} {
		t.Run("duplicate "+name, func(t *testing.T) {
			entries := append(pluginArchiveEntries(t, manifestFixture()), pluginArchiveEntry{name: name, body: []byte("duplicate")})
			_, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, entries)))
			registryProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
}

func TestResolveArchiveRequiresManifestAndActualDeclaredPatchFile(t *testing.T) {
	valid := pluginArchiveEntries(t, manifestFixture())
	cases := map[string][]pluginArchiveEntry{
		"missing manifest":      valid[1:],
		"wrong manifest path":   {{name: "package/manifest.json", body: valid[0].body}, valid[1]},
		"manifest is directory": {{name: "package/package.json", kind: tar.TypeDir}, valid[1]},
		"invalid manifest JSON": {{name: "package/package.json", body: []byte("{")}, valid[1]},
		"missing patch":         valid[:1],
		"different patch":       {valid[0], {name: "package/unrelated.yml", body: []byte("plugins: {}")}},
		"patch is directory":    {valid[0], {name: "package/cordis.patch.yml", kind: tar.TypeDir}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, entries)))
			registryProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
	manifest := manifestFixture()
	manifest["dsh"] = map[string]any{"bundle": map[string]string{"patch": "./config/cordis.patch.yml"}}
	entries := pluginArchiveEntries(t, manifest)
	entries[1].name = "package/config/cordis.patch.yml"
	if _, err := ResolveArchive(context.Background(), "plugin.tar.gz", pluginGzip(t, pluginTar(t, entries))); err != nil {
		t.Fatal("valid nested package-relative patch rejected", err)
	}
}

func TestResolveArchiveAppliesTheRegistryManifestRules(t *testing.T) {
	cases := map[string]func(map[string]any){
		"official scope": func(m map[string]any) { m["name"] = "@deepseek-ai/dsh-official" },
		"invalid name":   func(m map[string]any) { m["name"] = "Invalid Name" },
		"missing version": func(m map[string]any) {
			delete(m, "version")
		},
		"version range": func(m map[string]any) { m["version"] = "^1.2.3" },
		"version tag":   func(m map[string]any) { m["version"] = "latest" },
		"missing bundle": func(m map[string]any) {
			delete(m, "dsh")
		},
		"remote patch": func(m map[string]any) {
			m["dsh"] = map[string]any{"bundle": map[string]string{"patch": "https://example.com/patch.yml"}}
		},
		"parent patch": func(m map[string]any) {
			m["dsh"] = map[string]any{"bundle": map[string]string{"patch": "../patch.yml"}}
		},
		"absolute patch": func(m map[string]any) {
			m["dsh"] = map[string]any{"bundle": map[string]string{"patch": "/patch.yml"}}
		},
		"invalid keywords": func(m map[string]any) { m["keywords"] = 42 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := manifestFixture()
			change(manifest)
			_, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, pluginArchiveEntries(t, manifest))))
			registryProblem(t, err, 422, "PLUGIN_INVALID")
			registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				return manifestResponse(t, request, 200, manifest), nil
			})}}
			if _, err = registry.Resolve(context.Background(), "@community/context-memory@1.2.3-beta.1+build.4"); err == nil {
				t.Fatal("online registry accepted a manifest rejected offline")
			}
		})
	}
}

func TestResolveArchiveEnforcesCompressedExpandedAndManifestLimits(t *testing.T) {
	if _, err := ResolveArchive(context.Background(), "plugin.tgz", make([]byte, maxArchiveBytes+1)); err == nil {
		t.Fatal("oversized compressed archive accepted")
	} else {
		registryProblem(t, err, 413, "PLUGIN_TOO_LARGE")
	}
	if _, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, make([]byte, maxArchiveExpandedBytes+1))); err == nil {
		t.Fatal("oversized expanded archive accepted")
	} else {
		registryProblem(t, err, 413, "PLUGIN_TOO_LARGE")
	}
	valid := pluginArchiveEntries(t, manifestFixture())
	for _, size := range []int{registryLimit, registryLimit + 1} {
		t.Run("manifest "+strings.Repeat("+", size-registryLimit), func(t *testing.T) {
			entries := append([]pluginArchiveEntry(nil), valid...)
			entries[0].body = append(bytes.Clone(valid[0].body), bytes.Repeat([]byte{' '}, size-len(valid[0].body))...)
			_, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, entries)))
			if size == registryLimit && err != nil {
				t.Fatal("package.json at its limit rejected", err)
			}
			if size > registryLimit {
				registryProblem(t, err, 413, "PLUGIN_TOO_LARGE")
			}
		})
	}
	// Legal tar padding counts toward the expansion limit and is accepted at it.
	raw := pluginTar(t, valid)
	raw = append(raw, make([]byte, maxArchiveExpandedBytes-len(raw))...)
	if _, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, raw)); err != nil {
		t.Fatal("archive at its expanded limit rejected", err)
	}
}

func TestResolveArchiveCountsFilesAndDirectories(t *testing.T) {
	entries := pluginArchiveEntries(t, manifestFixture())
	for i := 2; i < maxArchiveEntries; i++ {
		entries = append(entries, pluginArchiveEntry{name: "package/dir-" + strconv.Itoa(i), kind: tar.TypeDir})
	}
	if _, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, entries))); err != nil {
		t.Fatal("archive at its entry limit rejected", err)
	}
	entries = append(entries, pluginArchiveEntry{name: "package/one-too-many"})
	_, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, pluginTar(t, entries)))
	registryProblem(t, err, 413, "PLUGIN_TOO_LARGE")
}

func TestResolveArchiveAcceptsTypeRegA(t *testing.T) {
	raw := pluginTar(t, pluginArchiveEntries(t, manifestFixture()))
	// A zero type flag is the older regular-file encoding. Recompute its checksum.
	raw[156] = 0
	for i := 148; i < 156; i++ {
		raw[i] = ' '
	}
	var sum int64
	for _, b := range raw[:512] {
		sum += int64(b)
	}
	// Use tar's own octal checksum representation from a fresh header.
	checksum := []byte{'0', '0', '0', '0', '0', '0', 0, ' '}
	for i := 5; i >= 0; i-- {
		checksum[i] = byte('0' + sum%8)
		sum /= 8
	}
	copy(raw[148:156], checksum)
	if _, err := ResolveArchive(context.Background(), "plugin.tgz", pluginGzip(t, raw)); err != nil {
		t.Fatal("legacy regular file rejected", err)
	}
}
