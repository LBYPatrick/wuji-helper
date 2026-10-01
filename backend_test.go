package main

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testZIP(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gboard-v0.10.1.ota.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range entries {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPackageSnapshot(t *testing.T) {
	path := testZIP(t, map[string]string{"manifest.json": `{"version":"0.10.1"}`, "firmware.bin": "firmware"})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := preparePackage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.cleanup()
	if p.Version != "0.10.1" || p.Size != int64(len(before)) || len(p.SHA256) != 64 {
		t.Fatalf("unexpected package: %+v", p)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(p.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("snapshot changed with original")
	}
	dir := p.dir
	p.cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("snapshot was not removed")
	}
}

func TestPackageRejections(t *testing.T) {
	for _, test := range []struct{ name, path string }{
		{"missing", filepath.Join(t.TempDir(), "missing.zip")},
		{"directory", t.TempDir()},
		{"empty archive", testZIP(t, map[string]string{})},
		{"invalid manifest", testZIP(t, map[string]string{"manifest.json": "broken"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if p, err := preparePackage(test.path); err == nil {
				p.cleanup()
				t.Fatal("accepted invalid package")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "not-a-package.zip")
	if err := os.WriteFile(path, []byte("not zip"), 0600); err != nil {
		t.Fatal(err)
	}
	if p, err := preparePackage(path); err == nil {
		p.cleanup()
		t.Fatal("accepted non-ZIP")
	}
}

func TestExpandHomeAndQuotedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := expandPath(`"~/package with spaces.ota.zip"`)
	if err != nil || path != filepath.Join(home, "package with spaces.ota.zip") {
		t.Fatalf("%q, %v", path, err)
	}
	for _, invalid := range []string{"", "~someone/file"} {
		if _, err := expandPath(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestProbeInfo(t *testing.T) {
	for _, input := range []string{
		`{"sn":"G1","device_type":"wuji_glove","firmware_version":"0.11.0"}`,
		`{"devices":[{"sn":"OTHER","device_type":"wuji_hand_2"},{"sn":"G1","device_type":"wuji_glove","firmware":"0.11.0"}]}`,
	} {
		kind, version, err := probeInfo([]byte(input), "G1")
		if err != nil || kind != "wuji_glove" || version != "0.11.0" {
			t.Fatalf("%s, %s, %v", kind, version, err)
		}
	}
	for _, input := range []string{`bad JSON`, `{"sn":"OTHER","device_type":"wuji_glove"}`, `{"sn":"G1"}`} {
		if _, _, err := probeInfo([]byte(input), "G1"); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}

func fakeCLI(t *testing.T) (cli, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wuji")
	trace := filepath.Join(dir, "trace")
	t.Setenv("WUJI_HELPER_TEST_TRACE", trace)
	script := `#!/bin/sh
case "$1" in
 devices)
 if [ "$WUJI_HELPER_TEST_TWO" = 1 ]; then
  echo '{"devices":[{"sn":"G1","transport":"Usb","address":"/dev/ttyACM0"},{"sn":"G2","transport":"Usb","address":"/dev/ttyACM1"}]}'
  exit 0
 fi
 printf '%s\n' '{"devices":[{"sn":"G1","transport":"Usb","address":"/dev/ttyACM0"},{"sn":"G1","transport":"Udp"},{"sn":"OFFLINE"},{"sn":"HAND"}]}' ;;
 ping)
 case "$3" in
 G1) printf '%s\n' '{"devices":[{"sn":"G1","device_type":"wuji_glove","firmware":"0.11.0"}]}' ;;
 G2) echo '{"sn":"G2","device_type":"wuji_glove","firmware":"0.11.0"}' ;;
 HAND) printf '%s\n' '{"sn":"HAND","device_type":"wuji_hand_2","firmware":"0.12.0"}' ;;
 *) echo 'probe failed' >&2; exit 1 ;;
 esac ;;
 get)
 if [ "$WUJI_HELPER_TEST_BAD_SIDE" = 1 ]; then echo '{"value":"unknown"}'; exit 0; fi
 case "$2" in
 hand_side)
 if [ "$4" = G2 ]; then echo '{"value":"right"}'; exit 0; fi
 echo '{"value":"left"}' ;;
 firmware_version) echo '{"value":"0.11.0"}' ;;
 *) exit 2 ;;
 esac ;;
 upgrade)
 printf '%s\n' "$@" >> "$WUJI_HELPER_TEST_TRACE"
 echo 'G1 ok 0.11.0 -> 0.10.1'
 if [ "$WUJI_HELPER_TEST_FAIL" = 1 ]; then echo 'flash failed' >&2; exit 1; fi ;;
 *) exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return cli{binary: path}, trace
}

func TestScanAndFlash(t *testing.T) {
	c, trace := fakeCLI(t)
	devices, err := c.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 3 || devices[0].Kind != "wuji_glove" || devices[0].Side != "left" || devices[0].Version != "0.11.0" || devices[1].Problem == "" || devices[2].Kind != "wuji_hand_2" {
		t.Fatalf("%+v", devices)
	}
	// Shell metacharacters and spaces must remain literal process arguments.
	p := &firmwarePackage{Snapshot: filepath.Join(t.TempDir(), "package $(false) with spaces.zip")}
	var output bytes.Buffer
	if err := c.flash(context.Background(), "G1", p, &output); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Join([]string{"upgrade", "--sn", "G1", "--file", p.Snapshot, "--yes", ""}, "\n")
	if string(args) != expected {
		t.Fatalf("unexpected invocation %q", args)
	}
	if !strings.Contains(output.String(), "ok") {
		t.Fatal("report not streamed")
	}
	t.Setenv("WUJI_HELPER_TEST_FAIL", "1")
	if err := c.flash(context.Background(), "G1", p, &output); err == nil {
		t.Fatal("flash error was swallowed")
	}
}

func TestInvalidHandednessIsUnavailable(t *testing.T) {
	c, _ := fakeCLI(t)
	t.Setenv("WUJI_HELPER_TEST_BAD_SIDE", "1")
	devices, err := c.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if devices[0].Problem == "" {
		t.Fatal("glove with unknown handedness was selectable")
	}
}
