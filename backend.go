package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type device struct {
	SN        string `json:"sn"`
	Transport string `json:"transport"`
	Address   string `json:"address"`
	Side      string
	Kind      string
	Version   string
	Problem   string
}

type cli struct{ binary string }

func (c cli) query(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.binary, args...)
	cmd.Env = append(os.Environ(), "WUJI_NO_UPDATE_CHECK=1", "NO_COLOR=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wuji %s: %w\n%s%s", strings.Join(args, " "), err, out, stderr.String())
	}
	return out, nil
}

func (c cli) scan(ctx context.Context) ([]device, error) {
	out, err := c.query(ctx, "devices", "--json")
	if err != nil {
		return nil, err
	}
	var report struct {
		Devices []device `json:"devices"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("decode discovery: %w", err)
	}
	seen := map[string]bool{}
	var devices []device
	for _, d := range report.Devices {
		if d.SN == "" || seen[d.SN] {
			continue
		}
		seen[d.SN] = true
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := c.query(probeCtx, "ping", "--sn", d.SN, "--json")
		cancel()
		if err != nil {
			d.Problem = err.Error()
		} else {
			d.Kind, d.Version, err = probeInfo(out, d.SN)
			if err == nil && strings.EqualFold(d.Kind, "wuji_glove") {
				d.Side, d.Version, err = c.gloveDetails(ctx, d.SN)
			}
			if err != nil {
				d.Problem = err.Error()
			}
		}
		devices = append(devices, d)
	}
	return devices, nil
}

// probeInfo accepts a single record or a report containing records. Identity and
// type must be returned by the handshake, never inferred from a serial prefix.
func probeInfo(data []byte, sn string) (string, string, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", "", fmt.Errorf("decode probe: %w", err)
	}
	var find func(any) (string, string, bool)
	find = func(v any) (string, string, bool) {
		switch v := v.(type) {
		case map[string]any:
			if v["sn"] == sn {
				kind, _ := v["device_type"].(string)
				version := "unknown"
				for _, key := range []string{"firmware_version", "firmware", "fw_version", "version"} {
					if s, ok := v[key].(string); ok && s != "" {
						version = s
						break
					}
				}
				if kind != "" {
					return kind, version, true
				}
			}
			for _, child := range v {
				if k, f, ok := find(child); ok {
					return k, f, true
				}
			}
		case []any:
			for _, child := range v {
				if k, f, ok := find(child); ok {
					return k, f, true
				}
			}
		}
		return "", "", false
	}
	if k, f, ok := find(value); ok {
		return k, f, nil
	}
	return "", "", fmt.Errorf("probe returned no device type for serial %s; update Wuji CLI or check its JSON output", sn)
}

// gloveDetails reads the public parameters rather than inferring handedness
// from serial numbers or relying on optional ping version fields.
func (c cli) gloveDetails(ctx context.Context, sn string) (string, string, error) {
	read := func(name string) (string, error) {
		queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := c.query(queryCtx, "get", name, "--sn", sn, "--json")
		if err != nil {
			return "", err
		}
		var report struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(out, &report); err != nil {
			return "", fmt.Errorf("decode %s: %w", name, err)
		}
		if strings.TrimSpace(report.Value) == "" {
			return "", fmt.Errorf("%s returned no value for %s", name, sn)
		}
		return report.Value, nil
	}
	side, err := read("hand_side")
	if err != nil {
		return "", "", err
	}
	side = strings.ToLower(side)
	if side != "left" && side != "right" {
		return "", "", fmt.Errorf("invalid glove hand_side %q for %s", side, sn)
	}
	version, err := read("firmware_version")
	return side, version, err
}

type firmwarePackage struct {
	Original string
	Snapshot string
	SHA256   string
	Size     int64
	Version  string
	dir      string
}

func expandPath(input string) (string, error) {
	path := strings.TrimSpace(input)
	if len(path) >= 2 && ((path[0] == '"' && path[len(path)-1] == '"') || (path[0] == '\'' && path[len(path)-1] == '\'')) {
		path = path[1 : len(path)-1]
	}
	if path == "" {
		return "", fmt.Errorf("enter a package path")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("use ~/ for your home directory")
	}
	return filepath.Abs(path)
}

func preparePackage(input string) (_ *firmwarePackage, err error) {
	path, err := expandPath(input)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open package: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const maxSize = 512 << 20
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxSize {
		return nil, fmt.Errorf("package must be a nonempty regular file of at most 512 MiB")
	}
	dir, err := os.MkdirTemp("", "wuji-helper-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	snapshot := filepath.Join(dir, "firmware.ota.zip")
	dst, err := os.OpenFile(snapshot, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(file, maxSize+1))
	closeErr := dst.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if size > maxSize {
		return nil, fmt.Errorf("package grew beyond 512 MiB")
	}
	archive, err := zip.OpenReader(snapshot)
	if err != nil {
		return nil, fmt.Errorf("expected an official Wuji OTA ZIP package: %w", err)
	}
	defer archive.Close()
	if len(archive.File) == 0 {
		return nil, fmt.Errorf("ZIP package is empty")
	}
	var total int64
	version := "unknown (verified by Wuji CLI during installation)"
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		reader, openErr := entry.Open()
		if openErr != nil {
			return nil, openErr
		}
		// Read through EOF to validate CRC without extracting archive paths.
		n, readErr := io.Copy(io.Discard, io.LimitReader(reader, maxSize-total+1))
		reader.Close()
		total += n
		if total > maxSize {
			return nil, fmt.Errorf("ZIP expands beyond 512 MiB")
		}
		if readErr != nil {
			return nil, fmt.Errorf("invalid ZIP entry %s: %w", entry.Name, readErr)
		}
		if filepath.Base(entry.Name) == "manifest.json" && entry.UncompressedSize64 <= 1<<20 {
			r, e := entry.Open()
			if e != nil {
				return nil, e
			}
			var manifest struct {
				Version string `json:"version"`
			}
			e = json.NewDecoder(r).Decode(&manifest)
			r.Close()
			if e != nil {
				return nil, fmt.Errorf("invalid package manifest: %w", e)
			}
			if manifest.Version != "" {
				version = manifest.Version
			}
		}
	}
	return &firmwarePackage{Original: path, Snapshot: snapshot, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size, Version: version, dir: dir}, nil
}

func (p *firmwarePackage) cleanup() {
	if p != nil {
		os.RemoveAll(p.dir)
	}
}

func (c cli) flash(ctx context.Context, sn string, p *firmwarePackage, output io.Writer) error {
	cmd := exec.CommandContext(ctx, c.binary, "upgrade", "--sn", sn, "--file", p.Snapshot, "--yes")
	cmd.Env = append(os.Environ(), "WUJI_NO_UPDATE_CHECK=1", "NO_COLOR=1")
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("flash %s: %w", sn, err)
	}
	return nil
}
