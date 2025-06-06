package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func (u *Updater) updateFromArchive(srcExec string) error {
	archiveData, err := u.downloadArchive()
	if err != nil {
		return fmt.Errorf("failed to download archive: %w", err)
	}

	tempDir, err := ioutil.TempDir("", "selfupdate")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	err = u.extractArchive(archiveData, tempDir)
	if err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}

	err = u.validateExtractedFiles(tempDir)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	err = u.replaceFiles(tempDir, srcExec)
	if err != nil {
		return err
	}

	if u.OnSuccessfulUpdate != nil {
		u.OnSuccessfulUpdate()
	}

	return nil
}

func (u *Updater) downloadArchive() (io.ReadCloser, error) {
	urlLink, err := url.JoinPath(u.BinURL, u.CmdName, url.QueryEscape(u.Info.Version), u.archiveName())
	if err != nil {
		return nil, fmt.Errorf("failed to construct download URL: %w", err)
	}

	if u.Requester == nil {
		return defaultHTTPRequester.Fetch(urlLink)
	}

	r, err := u.Requester.Fetch(urlLink)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	return r, nil
}

func (u *Updater) extractArchive(data io.ReadCloser, destDir string) error {
	archiveFormat := u.ArchiveFormat
	if archiveFormat == "" {
		if runtime.GOOS == "windows" {
			archiveFormat = "zip"
		} else {
			archiveFormat = "tar.gz"
		}
	}

	archiveData, err := io.ReadAll(data)
	if err != nil {
		return fmt.Errorf("failed to read archive data: %w", err)
	}

	switch archiveFormat {
	case "zip":
		return u.extractZip(archiveData, destDir)
	case "tar.gz":
		return u.extractTarGz(archiveData, destDir)
	default:
		return fmt.Errorf("unsupported archive format: %s", archiveFormat)
	}
}

func (u *Updater) extractZip(data []byte, destDir string) error {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return err
	}

	for _, file := range zipReader.File {
		err := u.extractZipFile(file, destDir)
		if err != nil {
			return err
		}
	}

	return nil
}

func (u *Updater) extractZipFile(file *zip.File, destDir string) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	path := filepath.Join(destDir, file.Name)

	// Security check
	if !strings.HasPrefix(path, filepath.Clean(destDir)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path: %s", file.Name)
	}

	if file.FileInfo().IsDir() {
		return os.MkdirAll(path, file.FileInfo().Mode())
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.FileInfo().Mode())
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, rc)
	return err
}

func (u *Updater) extractTarGz(data []byte, destDir string) error {
	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		path := filepath.Join(destDir, header.Name)

		// Security check
		if !strings.HasPrefix(path, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}

			outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			_, err = io.Copy(outFile, tarReader)
			outFile.Close()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (u *Updater) validateExtractedFiles(tempDir string) error {
	exeName := plat
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}

	exePath := filepath.Join(tempDir, u.unpackedArchiveName(), exeName)
	if _, err := os.Stat(exePath); os.IsNotExist(err) {
		return fmt.Errorf("executable not found: %s", exeName)
	}

	// Check extra files
	for _, extraFile := range u.ExtraFiles {
		filePath := filepath.Join(tempDir, u.unpackedArchiveName(), extraFile)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return fmt.Errorf("required file not found: %s", extraFile)
		}
	}

	return nil
}

func (u *Updater) replaceFiles(tempDir string, srcExec string) error {
	currentDir := filepath.Dir(srcExec)

	exeName := u.plat()
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}

	//replace BIN
	newBinPath := filepath.Join(tempDir, u.unpackedArchiveName(), exeName)
	err := u.replaceFile(newBinPath, srcExec)
	if err != nil {
		return fmt.Errorf("cannot replaceFileFromStream. Err: %s, File: %s", err.Error(), newBinPath)
	}

	// Add extra files
	var replacements []struct{ src, dst string }

	for _, extraFile := range u.ExtraFiles {
		src := filepath.Join(tempDir, u.unpackedArchiveName(), extraFile)
		dst := filepath.Join(currentDir, extraFile)

		replacements = append(replacements, struct{ src, dst string }{
			src: src,
			dst: dst,
		})
	}

	backups := make(map[string]string)
	for _, repl := range replacements {
		if _, err := os.Stat(repl.dst); err == nil {
			backupPath := repl.dst + ".backup"
			err := u.copyFile(repl.dst, backupPath)
			if err != nil {
				u.restoreBackups(backups)
				return fmt.Errorf("failed to create backup for %s: %w", repl.dst, err)
			}
			backups[repl.dst] = backupPath
		}
	}

	for _, repl := range replacements {
		err := u.replaceFile(repl.src, repl.dst)
		if err != nil {
			u.restoreBackups(backups)
			return fmt.Errorf("failed to replace %s: %w", repl.dst, err)
		}
	}

	u.cleanupBackups(backups)

	return nil
}

func (u *Updater) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, sourceInfo.Mode())
}

func (u *Updater) replaceFile(src, dst string) error {
	newBuf, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("cannot read file before replace : %s", src)
	}

	newFileBuffer := bytes.NewBuffer(newBuf)

	err, errRecovery := replaceFileFromStream(newFileBuffer, dst)
	if errRecovery != nil {
		return fmt.Errorf("update and recovery errors: %q %q", err, errRecovery)
	}

	return err
}

func (u *Updater) restoreBackups(backups map[string]string) {
	for original, backup := range backups {
		if _, err := os.Stat(backup); err == nil {
			os.Rename(backup, original)
		}
	}
}

func (u *Updater) cleanupBackups(backups map[string]string) {
	for _, backup := range backups {
		os.Remove(backup)
	}
}

func (u *Updater) plat() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func (u *Updater) archiveName() string {
	archiveFormat := u.ArchiveFormat
	if archiveFormat == "" {
		if runtime.GOOS == "windows" {
			archiveFormat = "zip"
		} else {
			archiveFormat = "tar.gz"
		}
	}

	return fmt.Sprintf("%s.%s", u.plat(), archiveFormat)
}

func (u *Updater) unpackedArchiveName() string {
	return u.plat()
}

func GetSharedLibConfig() (sharedLibName, archiveFormat string, err error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	switch goos {
	case "windows":
		sharedLibName = "gridnetlibamd64.dll"
		archiveFormat = "zip"
	case "darwin":
		switch goarch {
		case "arm64":
			sharedLibName = "darwingridnetlibarm64.dylib"
			archiveFormat = "tar.gz"
		case "amd64":
			sharedLibName = "darwingridnetlibamd64.dylib"
			archiveFormat = "tar.gz"
		default:
			err = fmt.Errorf("unsupported architecture %s for Darwin", goarch)
		}

	case "linux":
		sharedLibName = "libgridnetlib.so"
		archiveFormat = "tar.gz"

	default:
		err = fmt.Errorf("unsupported operating system: %s", goos)
	}

	return
}
