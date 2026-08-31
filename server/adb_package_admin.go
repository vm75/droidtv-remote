package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type ADBPackageConfirmation struct {
	TVID        string `json:"tv_id"`
	PackageID   string `json:"package_id"`
	Action      string `json:"action"`
	CurrentUser *int   `json:"current_user"`
	Enabled     *bool  `json:"enabled"`
}

type ADBPackageMutationRequest struct {
	PackageID    string                 `json:"package_id"`
	Confirmation ADBPackageConfirmation `json:"confirmation"`
}

var protectedADBPackages = map[string]struct{}{
	"android":                              {},
	"com.android.adbd":                     {},
	"com.android.inputmethod.latin":        {},
	"com.android.packageinstaller":         {},
	"com.android.settings":                 {},
	"com.android.systemui":                 {},
	"com.android.tv.settings":              {},
	"com.google.android.apps.tv.launcherx": {},
	"com.google.android.gms":               {},
	"com.google.android.gsf":               {},
	"com.google.android.inputmethod.latin": {},
	"com.google.android.leanbacklauncher":  {},
	"com.google.android.packageinstaller":  {},
	"com.google.android.tvlauncher":        {},
}

func isProtectedADBPackage(packageID string) bool {
	_, ok := protectedADBPackages[packageID]
	return ok
}

func findADBPackage(inventory ADBPackageInventory, packageID string) (ADBPackage, bool) {
	for _, pkg := range inventory.Packages {
		if pkg.PackageID == packageID {
			return pkg, true
		}
	}
	return ADBPackage{}, false
}

func validateADBPackageID(packageID string) error {
	packageID = strings.TrimSpace(packageID)
	if packageID == "" || len(packageID) > 255 || !adbPackageIDPattern.MatchString(packageID) {
		return &ADBError{Code: "invalid_package", Message: "A valid package selected from ADB inventory is required"}
	}
	return nil
}

func (m *ADBManager) verifyThirdPartyPackagePath(ctx context.Context, serial string, user int, packageID string) error {
	result, err := m.deviceCommand(ctx, serial, "shell", "pm", "path", "--user", strconv.Itoa(user), packageID)
	if err != nil {
		return err
	}
	if result.Truncated {
		return &ADBError{Code: "package_state_unavailable", Message: "Package path output was truncated; mutation was not attempted"}
	}
	found := false
	for _, raw := range strings.Split(result.Stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "package:") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "package:"))
		if path == "" {
			continue
		}
		found = true
		if !strings.HasPrefix(path, "/data/app/") {
			return &ADBError{Code: "protected_package", Message: "System or privileged packages cannot be administered"}
		}
	}
	if !found {
		return &ADBError{Code: "package_state_unavailable", Message: "Package installation path could not be verified"}
	}
	return nil
}

func validateADBPackageConfirmation(tvID, action string, pkg ADBPackage, user int, confirmation ADBPackageConfirmation) error {
	if confirmation.TVID != tvID ||
		confirmation.PackageID != pkg.PackageID ||
		confirmation.Action != action ||
		confirmation.CurrentUser == nil ||
		*confirmation.CurrentUser != user ||
		confirmation.Enabled == nil ||
		pkg.Enabled == nil ||
		*confirmation.Enabled != *pkg.Enabled {
		return &ADBError{Code: "stale_package_state", Message: "Package state changed or confirmation no longer matches fresh inventory; refresh discovery and try again"}
	}
	return nil
}

func (m *ADBManager) mutatePackage(ctx context.Context, serial string, user int, packageID, action string) error {
	var args []string
	switch action {
	case "clear":
		args = []string{"shell", "pm", "clear", "--user", strconv.Itoa(user), packageID}
	case "enable":
		args = []string{"shell", "pm", "enable", "--user", strconv.Itoa(user), packageID}
	case "disable":
		args = []string{"shell", "pm", "disable-user", "--user", strconv.Itoa(user), packageID}
	case "uninstall":
		args = []string{"shell", "pm", "uninstall", "--user", strconv.Itoa(user), packageID}
	default:
		return &ADBError{Code: "invalid_package_action", Message: "Unsupported package administration action"}
	}
	result, err := m.deviceCommand(ctx, serial, args...)
	if err != nil {
		return err
	}
	text := strings.ToLower(strings.TrimSpace(result.Stdout + "\n" + result.Stderr))
	if strings.Contains(text, "failure") || strings.Contains(text, "failed") || strings.Contains(text, "error:") {
		return &ADBError{Code: "package_mutation_failed", Message: "Android Package Manager rejected the package administration action"}
	}
	return nil
}

func (s *Server) reconcileADBPackageLauncherAvailability(tvID, packageID string, available bool) (bool, error) {
	if available {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tv := s.tvs[tvID]
	if tv == nil {
		return false, fmt.Errorf("Unknown TV")
	}
	next := make([]string, 0, len(tv.AppIDs))
	changed := false
	for _, appID := range tv.AppIDs {
		app := s.apps[appID]
		if app != nil && app.PackageID == packageID {
			changed = true
			continue
		}
		next = append(next, appID)
	}
	if !changed {
		return false, nil
	}
	previous := append([]string(nil), tv.AppIDs...)
	tv.AppIDs = next
	if err := s.saveTVsLocked(); err != nil {
		tv.AppIDs = previous
		return false, &ADBError{Code: "partial_failure", Message: "Package state changed, but selected-TV launcher availability could not be persisted"}
	}
	return true, nil
}

func (s *Server) adbPackageMutation(ctx context.Context, tvID, action string, request ADBPackageMutationRequest) (map[string]any, error) {
	if _, err := s.copyTV(tvID); err != nil {
		return nil, err
	}
	if err := validateADBPackageID(request.PackageID); err != nil {
		return nil, err
	}
	if isProtectedADBPackage(request.PackageID) {
		return nil, &ADBError{Code: "protected_package", Message: "This core Android/Google TV package is protected from ADB administration"}
	}

	state := s.state(tvID)
	state.adbPackageMu.Lock()
	defer state.adbPackageMu.Unlock()

	serial, err := s.requireADBConnected(ctx, tvID)
	if err != nil {
		return nil, err
	}
	before, err := s.adb.PackageInventory(ctx, serial)
	if err != nil {
		return nil, err
	}
	if before.Truncated {
		return nil, &ADBError{Code: "package_state_unavailable", Message: "Package inventory is truncated; mutation was not attempted"}
	}
	pkg, found := findADBPackage(before, request.PackageID)
	if !found {
		return nil, &ADBError{Code: "package_not_found", Message: "Package is not installed for the current Android user"}
	}
	if pkg.Classification != "third_party" || !pkg.ThirdParty || pkg.System {
		return nil, &ADBError{Code: "protected_package", Message: "Only freshly discovered third-party packages can be administered"}
	}
	if pkg.Enabled == nil {
		return nil, &ADBError{Code: "package_state_unavailable", Message: "Package enabled state is unavailable; mutation was not attempted"}
	}
	if err := s.adb.verifyThirdPartyPackagePath(ctx, serial, before.CurrentUser, pkg.PackageID); err != nil {
		return nil, err
	}
	if err := validateADBPackageConfirmation(tvID, action, pkg, before.CurrentUser, request.Confirmation); err != nil {
		return nil, err
	}

	if err := s.adb.mutatePackage(ctx, serial, before.CurrentUser, pkg.PackageID, action); err != nil {
		return nil, err
	}

	after, err := s.adb.PackageInventory(ctx, serial)
	if err != nil {
		return nil, &ADBError{Code: "partial_failure", Message: "Package command completed, but resulting package state could not be verified"}
	}
	if after.Truncated {
		return nil, &ADBError{Code: "partial_failure", Message: "Package command completed, but resulting package inventory was truncated"}
	}
	resultPkg, installed := findADBPackage(after, pkg.PackageID)
	switch action {
	case "clear":
		if !installed {
			return nil, &ADBError{Code: "partial_failure", Message: "Clear-data command completed, but the package disappeared from current-user inventory"}
		}
	case "enable":
		if !installed || resultPkg.Enabled == nil || !*resultPkg.Enabled {
			return nil, &ADBError{Code: "package_mutation_failed", Message: "Enable command did not produce a verified enabled package state"}
		}
	case "disable":
		if !installed || resultPkg.Enabled == nil || *resultPkg.Enabled {
			return nil, &ADBError{Code: "package_mutation_failed", Message: "Disable command did not produce a verified disabled package state"}
		}
	case "uninstall":
		if installed {
			return nil, &ADBError{Code: "package_mutation_failed", Message: "Uninstall command did not remove the package for the current Android user"}
		}
	}

	available := installed
	if action == "disable" {
		available = false
	}
	reconciled, err := s.reconcileADBPackageLauncherAvailability(tvID, pkg.PackageID, available)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"tv_id":                          tvID,
		"action":                         action,
		"package_id":                     pkg.PackageID,
		"current_user":                   after.CurrentUser,
		"installed":                      installed,
		"launcher_availability_changed":  reconciled,
	}
	if installed {
		result["package"] = resultPkg
	}
	return result, nil
}

func decodeADBPackageMutation(r *http.Request) (ADBPackageMutationRequest, error) {
	var in ADBPackageMutationRequest
	if err := decodeJSON(r, &in); err != nil {
		return in, &ADBError{Code: "invalid_package", Message: "Invalid package administration request"}
	}
	return in, nil
}

func (s *Server) handleADBPackageAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adbNoStore(w)
		if err := s.requireADBAuthorization(r); err != nil {
			writeADBError(w, err)
			return
		}
		in, err := decodeADBPackageMutation(r)
		if err != nil {
			writeADBError(w, err)
			return
		}
		result, err := s.adbPackageMutation(r.Context(), r.PathValue("tv_id"), action, in)
		if err != nil {
			writeADBError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, result)
	}
}
