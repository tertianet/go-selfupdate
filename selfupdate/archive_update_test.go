package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateFromArchive_Success(t *testing.T) {
	data := fakeTarGzArchive(t)

	mr := &mockRequester{}
	mr.handleRequest(func(url string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})

	updater := createTestUpdater("tar.gz", mr)

	// Create a temporary file path to simulate the binary location
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "myapp")

	// Create a dummy binary file to simulate existing binary
	err := os.WriteFile(targetPath, []byte("old binary"), 0755)
	if err != nil {
		t.Fatalf("Failed to create dummy binary: %v", err)
	}

	err = updater.updateFromArchive(targetPath)
	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}

	// Add validation if needed, like checking file content or mod time
}

func TestUpdateFromArchive_DownloadError(t *testing.T) {
	mr := &mockRequester{}
	mr.handleRequest(func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("network error")
	})

	updater := createTestUpdater("tar.gz", mr)
	err := updater.updateFromArchive("/usr/bin/myapp")
	if err == nil || !strings.Contains(err.Error(), "failed to download archive") {
		t.Fatalf("Expected download error, got: %v", err)
	}
}

func TestUpdateFromArchive_InvalidArchive(t *testing.T) {
	mr := &mockRequester{}
	mr.handleRequest(func(url string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader([]byte("corrupted"))), nil
	})

	updater := createTestUpdater("tar.gz", mr)
	err := updater.updateFromArchive("/usr/bin/myapp")
	if err == nil || !strings.Contains(err.Error(), "failed to extract archive") {
		t.Fatalf("Expected extract error, got: %v", err)
	}
}

func TestUpdateFromArchive_ReplaceFilesFailure(t *testing.T) {
	data := fakeTarGzArchive(t)

	mr := &mockRequester{}
	mr.handleRequest(func(url string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})

	updater := createTestUpdater("tar.gz", mr)

	// Create a temporary directory for the test binary path
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "myapp")

	// Simulate a file that can't be replaced by making it read-only
	err := os.WriteFile(targetPath, []byte("dummy"), 0444)
	if err != nil {
		t.Fatalf("Failed to create read-only dummy binary: %v", err)
	}

	err = updater.updateFromArchive(targetPath)
	if err == nil || !strings.Contains(err.Error(), "replace") {
		t.Fatalf("Expected replace error, got: %v", err)
	}
}

func fakeTarGzArchive(t *testing.T) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := []byte("fake-binary")
	hdr := &tar.Header{
		Name: "myapp",
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func fakeZipArchive(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.Create("myapp.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("fake-binary")); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return buf.Bytes()
}

func createTestUpdater(format string, requester Requester) *Updater {
	return &Updater{
		CmdName:            "myapp",
		ArchiveFormat:      format,
		Requester:          requester,
		CurrentVersion:     "1.0.0",
		Dir:                ".",
		OnSuccessfulUpdate: func() {},
	}
}
