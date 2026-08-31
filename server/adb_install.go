package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxAPKFilenameBytes  = 255
	maxMCPAPKBytes       = 8 * 1024 * 1024
	maxMCPRequestBytes   = 16 * 1024 * 1024
	apkMultipartOverhead = 1024 * 1024
)

type adbAPKTemp struct {
	Path     string
	Filename string
	SHA256   string
	Size     int64
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}

func invalidAPKUpload(message string) error {
	return &ADBError{Code: "invalid_upload", Message: message}
}

func validateAPKFilename(filename string) error {
	if filename == "" || len(filename) > maxAPKFilenameBytes {
		return invalidAPKUpload("Upload must contain one valid .apk filename")
	}
	hasControl := strings.IndexFunc(filename, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0
	if filename != filepath.Base(filename) || strings.ContainsAny(filename, "/\\") || hasControl {
		return invalidAPKUpload("APK filename must not contain a path or control characters")
	}
	if !strings.EqualFold(filepath.Ext(filename), ".apk") || strings.TrimSuffix(filename, filepath.Ext(filename)) == "" {
		return invalidAPKUpload("Only a single .apk file can be installed")
	}
	return nil
}

func streamAPKToTemp(ctx context.Context, tempDir, filename string, src io.Reader, limit int64) (artifact adbAPKTemp, err error) {
	if err := validateAPKFilename(filename); err != nil {
		return adbAPKTemp{}, err
	}
	if limit <= 0 {
		return adbAPKTemp{}, &ADBError{Code: "upload_too_large", Message: "APK upload limit is not configured"}
	}
	if limit < 4 {
		return adbAPKTemp{}, &ADBError{Code: "upload_too_large", Message: "APK exceeds the configured upload-size limit"}
	}
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	file, err := os.CreateTemp(tempDir, "droidtv-remote-apk-*.apk")
	if err != nil {
		return adbAPKTemp{}, &ADBError{Code: "unavailable", Message: "Temporary APK storage is unavailable"}
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		return adbAPKTemp{}, &ADBError{Code: "unavailable", Message: "Temporary APK storage could not be secured"}
	}

	reader := contextReader{ctx: ctx, r: src}
	signature := make([]byte, 4)
	if _, err := io.ReadFull(reader, signature); err != nil {
		if ctx.Err() != nil {
			return adbAPKTemp{}, &ADBError{Code: "canceled", Message: "APK upload was canceled"}
		}
		return adbAPKTemp{}, &ADBError{Code: "invalid_apk", Message: "APK upload is empty or truncated"}
	}
	if string(signature) != "PK\x03\x04" {
		return adbAPKTemp{}, &ADBError{Code: "invalid_apk", Message: "Uploaded file does not have an APK/ZIP signature"}
	}

	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	if _, err := writer.Write(signature); err != nil {
		return adbAPKTemp{}, &ADBError{Code: "unavailable", Message: "Temporary APK storage failed"}
	}
	remaining := limit - int64(len(signature))
	copied, copyErr := io.CopyBuffer(writer, io.LimitReader(reader, remaining+1), make([]byte, 32*1024))
	if copyErr != nil {
		if ctx.Err() != nil || errors.Is(copyErr, context.Canceled) || errors.Is(copyErr, context.DeadlineExceeded) {
			return adbAPKTemp{}, &ADBError{Code: "canceled", Message: "APK upload was canceled"}
		}
		return adbAPKTemp{}, invalidAPKUpload("APK upload could not be read")
	}
	size := int64(len(signature)) + copied
	if size > limit {
		return adbAPKTemp{}, &ADBError{Code: "upload_too_large", Message: "APK exceeds the configured upload-size limit"}
	}
	if err := file.Close(); err != nil {
		return adbAPKTemp{}, &ADBError{Code: "unavailable", Message: "Temporary APK storage failed"}
	}
	keep = true
	return adbAPKTemp{
		Path:     path,
		Filename: filename,
		SHA256:   hex.EncodeToString(hash.Sum(nil)),
		Size:     size,
	}, nil
}

func multipartAPKFilename(part *multipart.Part) (string, error) {
	mediaType, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil || mediaType != "form-data" || params["name"] != "apk" {
		return "", invalidAPKUpload("Multipart request must contain exactly one file field named apk")
	}
	filename, ok := params["filename"]
	if !ok {
		return "", invalidAPKUpload("Multipart apk field must contain a filename")
	}
	if err := validateAPKFilename(filename); err != nil {
		return "", err
	}
	return filename, nil
}

func receiveMultipartAPK(w http.ResponseWriter, r *http.Request, tempDir string, limit int64) (adbAPKTemp, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return adbAPKTemp{}, invalidAPKUpload("APK install requires multipart/form-data")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+apkMultipartOverhead)
	reader := multipart.NewReader(r.Body, params["boundary"])
	part, err := reader.NextPart()
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return adbAPKTemp{}, &ADBError{Code: "upload_too_large", Message: "APK exceeds the configured upload-size limit"}
		}
		return adbAPKTemp{}, invalidAPKUpload("Multipart request must contain exactly one APK")
	}
	defer part.Close()
	filename, err := multipartAPKFilename(part)
	if err != nil {
		return adbAPKTemp{}, err
	}
	artifact, err := streamAPKToTemp(r.Context(), tempDir, filename, part, limit)
	if err != nil {
		return adbAPKTemp{}, err
	}
	next, nextErr := reader.NextPart()
	if next != nil {
		_ = next.Close()
		_ = os.Remove(artifact.Path)
		return adbAPKTemp{}, invalidAPKUpload("Multipart request must not contain extra parts")
	}
	if nextErr != io.EOF {
		_ = os.Remove(artifact.Path)
		var maxErr *http.MaxBytesError
		if errors.As(nextErr, &maxErr) {
			return adbAPKTemp{}, &ADBError{Code: "upload_too_large", Message: "APK exceeds the configured upload-size limit"}
		}
		return adbAPKTemp{}, invalidAPKUpload("Multipart request is malformed")
	}
	return artifact, nil
}

func mapAPKInstallFailure(result ADBResult, err error) error {
	if mapped := mapADBOperationError(result, err); mapped != nil {
		var adbErr *ADBError
		if errors.As(mapped, &adbErr) && adbErr.Code != "command_failed" {
			return mapped
		}
	}
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	switch {
	case strings.Contains(text, "install_failed_insufficient_storage"),
		strings.Contains(text, "insufficient storage"),
		strings.Contains(text, "not enough space"):
		return &ADBError{Code: "insufficient_storage", Message: "The TV does not have enough storage for this APK"}
	case strings.Contains(text, "install_failed_no_matching_abis"),
		strings.Contains(text, "no matching abis"):
		return &ADBError{Code: "incompatible_abi", Message: "The APK does not support this TV's CPU ABI"}
	case strings.Contains(text, "install_failed_older_sdk"),
		strings.Contains(text, "install_failed_newer_sdk"),
		strings.Contains(text, "requires newer sdk"),
		strings.Contains(text, "sdk version"):
		return &ADBError{Code: "incompatible_sdk", Message: "The APK is incompatible with this TV's Android SDK level"}
	case strings.Contains(text, "install_parse_failed"),
		strings.Contains(text, "install_failed_invalid_apk"),
		strings.Contains(text, "failed to parse"),
		strings.Contains(text, "not a valid zip"):
		return &ADBError{Code: "malformed_apk", Message: "Android rejected the APK as malformed"}
	case strings.Contains(text, "install_failed_update_incompatible"),
		strings.Contains(text, "install_failed_shared_user_incompatible"),
		strings.Contains(text, "signatures do not match"),
		strings.Contains(text, "inconsistent certificates"):
		return &ADBError{Code: "signature_mismatch", Message: "The installed app and uploaded APK do not have a compatible signing identity"}
	case strings.Contains(text, "install_failed_version_downgrade"),
		strings.Contains(text, "version downgrade"):
		return &ADBError{Code: "version_downgrade", Message: "APK version downgrade is not allowed"}
	}
	if err != nil {
		var adbErr *ADBError
		if errors.As(err, &adbErr) && adbErr.Code == "timeout" {
			return err
		}
	}
	return &ADBError{Code: "package_manager_failure", Message: "Android Package Manager rejected the APK"}
}

func (m *ADBManager) InstallAPK(ctx context.Context, serial, path string) (ADBResult, error) {
	serial = strings.TrimSpace(serial)
	if serial == "" || strings.HasPrefix(serial, "-") || strings.ContainsAny(serial, "\r\n\t ") {
		return ADBResult{}, &ADBError{Code: "invalid_target", Message: "An explicit ADB device serial is required"}
	}
	result, err := m.runWithTimeout(ctx, m.cfg.InstallTimeout, "-s", serial, "install", "-r", path)
	if err != nil {
		return result, mapAPKInstallFailure(result, err)
	}
	if !strings.Contains(strings.ToLower(result.Stdout+"\n"+result.Stderr), "success") {
		return result, mapAPKInstallFailure(result, nil)
	}
	return result, nil
}

func packageMap(inventory ADBPackageInventory) map[string]ADBPackage {
	out := make(map[string]ADBPackage, len(inventory.Packages))
	for _, pkg := range inventory.Packages {
		out[pkg.PackageID] = pkg
	}
	return out
}

func identifyInstalledPackage(before, after ADBPackageInventory) (ADBPackage, string, bool) {
	beforeMap := packageMap(before)
	candidates := make([]struct {
		pkg       ADBPackage
		operation string
	}, 0)
	for _, pkg := range after.Packages {
		previous, existed := beforeMap[pkg.PackageID]
		if !existed {
			candidates = append(candidates, struct {
				pkg       ADBPackage
				operation string
			}{pkg: pkg, operation: "install"})
			continue
		}
		if pkg.VersionCode != previous.VersionCode && (pkg.VersionCode != "" || previous.VersionCode != "") {
			candidates = append(candidates, struct {
				pkg       ADBPackage
				operation string
			}{pkg: pkg, operation: "update"})
		}
	}
	if len(candidates) != 1 {
		return ADBPackage{}, "unknown", false
	}
	return candidates[0].pkg, candidates[0].operation, true
}

func (s *Server) requireADBConnected(ctx context.Context, id string) (string, error) {
	state, err := s.adbState(ctx, id)
	if err != nil {
		return "", err
	}
	status, _ := state["state"].(string)
	switch status {
	case "connected":
		return s.adbTargetSerial(id)
	case "unauthorized":
		return "", &ADBError{Code: "unauthorized_device", Message: "The TV has not authorized this ADB host"}
	case "disabled":
		return "", &ADBError{Code: "disabled", Message: "ADB integration is disabled"}
	case "unavailable":
		return "", &ADBError{Code: "unavailable", Message: "ADB is unavailable"}
	default:
		return "", &ADBError{Code: "offline", Message: "The selected TV is not connected over ADB"}
	}
}

func (s *Server) installAPKArtifact(ctx context.Context, id string, artifact adbAPKTemp) (map[string]any, error) {
	if _, err := s.copyTV(id); err != nil {
		return nil, err
	}
	state := s.state(id)
	state.adbAdminMu.Lock()
	defer state.adbAdminMu.Unlock()

	serial, err := s.requireADBConnected(ctx, id)
	if err != nil {
		return nil, err
	}

	warnings := []string{}
	before, beforeErr := s.adb.PackageInventory(ctx, serial)
	if beforeErr != nil {
		warnings = appendWarning(warnings, "Pre-install package inventory was unavailable; install/update classification may be unknown.")
	}

	if _, err := s.adb.InstallAPK(ctx, serial, artifact.Path); err != nil {
		return nil, err
	}

	after, afterErr := s.adb.PackageInventory(ctx, serial)
	if afterErr != nil {
		warnings = appendWarning(warnings, "APK installed successfully, but refreshed package inventory was unavailable.")
	}

	operation := "unknown"
	result := map[string]any{
		"tv_id":      id,
		"status":     "success",
		"operation":  operation,
		"sha256":     artifact.SHA256,
		"size_bytes": artifact.Size,
	}
	if beforeErr == nil && afterErr == nil {
		if pkg, op, ok := identifyInstalledPackage(before, after); ok {
			operation = op
			result["operation"] = op
			result["package"] = pkg
		} else {
			warnings = appendWarning(warnings, "Install succeeded, but the affected package could not be uniquely identified from package inventory.")
		}
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

func (s *Server) handleADBInstallAPK(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	artifact, err := receiveMultipartAPK(w, r, s.adbTempDir, s.adb.cfg.APKMaxBytes)
	if err != nil {
		writeADBError(w, err)
		return
	}
	defer os.Remove(artifact.Path)

	result, err := s.installAPKArtifact(r.Context(), r.PathValue("tv_id"), artifact)
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) installAPKBase64(ctx context.Context, id, filename, encoded string) (map[string]any, error) {
	if encoded == "" {
		return nil, invalidAPKUpload("apk_base64 is required")
	}
	maxEncoded := base64.StdEncoding.EncodedLen(maxMCPAPKBytes) + 4
	if len(encoded) > maxEncoded {
		return nil, &ADBError{Code: "upload_too_large", Message: "MCP APK payload exceeds the 8 MiB decoded limit"}
	}
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	artifact, err := streamAPKToTemp(ctx, s.adbTempDir, filename, decoder, maxMCPAPKBytes)
	if err != nil {
		return nil, err
	}
	defer os.Remove(artifact.Path)
	return s.installAPKArtifact(ctx, id, artifact)
}
