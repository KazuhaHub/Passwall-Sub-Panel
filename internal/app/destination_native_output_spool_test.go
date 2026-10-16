//go:build destination_acceptance && linux

package app

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativePrivateSpoolName(name string) bool {
	return name != "" && filepath.Base(name) == name && !filepath.IsAbs(name) && strings.HasSuffix(name, "-output.private.log") && strings.IndexFunc(name, func(c rune) bool { return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') }) < 0
}

func nativePrivateLogLeak(reader io.Reader, private []string) (bool, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		for _, needle := range private {
			if needle != "" && strings.Contains(scanner.Text(), needle) {
				return true, nil
			}
		}
	}
	return false, scanner.Err()
}

// Install before starting the daemon. Register from the transport constructor
// so the cohort's later cleanup joins every writer before this complete-file
// scan and close. Private fields are resolved at cleanup, after provisioning.
func nativeSpoolPrivateOutput(t *testing.T, output *nativePrivateOutput, name string, private func() []string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	if err != nil || !filepath.IsAbs(root) || !nativePrivateSpoolName(name) || output == nil || private == nil {
		t.Fatal("private output spool scope invalid")
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.spill != nil || output.buf.Len() != 0 {
		t.Fatal("private output spool must precede daemon output")
	}
	file, err := os.OpenFile(filepath.Join(root, name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	nativeMust(t, err, "create fresh owned private daemon output")
	output.spill, output.retain = file, 64*1024
	t.Cleanup(func() {
		defer file.Close()
		nativeMust(t, file.Sync(), "flush complete private daemon output")
		info, err := file.Stat()
		nativeMust(t, err, "read private daemon archive size")
		needles := append([]string(nil), private()...)
		needles = append(needles, " accepted ", " rejected ")
		leaked, err := nativePrivateLogLeak(io.NewSectionReader(file, 0, info.Size()), needles)
		nativeMust(t, err, "scan complete private daemon output")
		if leaked {
			t.Error("daemon exposed private data in its complete output archive")
		}
	})
}

func TestNativeLongOutputSpoolKeepsFullPrivateArchiveAndBoundedTail(t *testing.T) {
	t.Run("full_archive_and_bounded_tail", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("RUNNER_TEMP", root)
		output := &nativePrivateOutput{}
		nativeSpoolPrivateOutput(t, output, "unit-output.private.log", func() []string { return []string{"never-in-log"} })
		data := bytes.Repeat([]byte("numeric pressure sample\n"), 10000)
		for start := 0; start < len(data); start += 113 {
			end := min(start+113, len(data))
			n, err := output.Write(data[start:end])
			if err != nil || n != end-start {
				t.Fatal("output spool lost a write")
			}
		}
		file := filepath.Join(root, "unit-output.private.log")
		actual, err := os.ReadFile(file)
		nativeMust(t, err, "read actual complete private spool")
		info, err := os.Stat(file)
		nativeMust(t, err, "read actual private spool mode")
		if !bytes.Equal(actual, data) || output.text() != string(data[len(data)-64*1024:]) || info.Mode().Perm() != 0600 {
			t.Fatal("private archive, memory bound or file mode changed")
		}
	})
	t.Run("full_scan_detects_early_and_split_secrets", func(t *testing.T) {
		data := append([]byte("early-private-token\n"), bytes.Repeat([]byte("numeric pressure\n"), 10000)...)
		if bytes.Contains(data[len(data)-64*1024:], []byte("early-private-token")) {
			t.Fatal("secret vector unexpectedly remained in memory tail")
		}
		for _, reader := range []io.Reader{bytes.NewReader(data), io.MultiReader(strings.NewReader("early-private-"), strings.NewReader("token\n"))} {
			leak, err := nativePrivateLogLeak(reader, []string{"early-private-token"})
			if err != nil || !leak {
				t.Fatal("complete-file scanner missed secret outside tail or split across writes")
			}
		}
		leak, err := nativePrivateLogLeak(bytes.NewReader(data), []string{"", "absent"})
		if err != nil || leak {
			t.Fatal("scanner invented private data")
		}
	})
	t.Run("invalid_names_and_oversized_lines_are_rejected", func(t *testing.T) {
		for _, name := range []string{"", "../escape-output.private.log", "/tmp/escape-output.private.log", "nested/file-output.private.log", "C:\\escape-output.private.log", "public.log", "Name-output.private.log"} {
			if nativePrivateSpoolName(name) {
				t.Fatal("output spool escaped its private filename scope")
			}
		}
		if !nativePrivateSpoolName("trial-day-node-09-output.private.log") || !nativePrivateSpoolName("retention-node-output.private.log") {
			t.Fatal("dedicated private spool filename rejected")
		}
		if leak, err := nativePrivateLogLeak(strings.NewReader(strings.Repeat("x", 65*1024)), []string{"secret"}); err == nil || leak {
			t.Fatal("overlong unscanned output line accepted")
		}
	})
}
