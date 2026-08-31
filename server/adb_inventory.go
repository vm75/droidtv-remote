package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxADBPackageRecords   = 5000
	maxADBLaunchableRecords = 2000
)

type ADBDeviceInfo struct {
	Manufacturer string   `json:"manufacturer,omitempty"`
	Model        string   `json:"model,omitempty"`
	Product      string   `json:"product,omitempty"`
	Android      string   `json:"android_release,omitempty"`
	APILevel     int      `json:"api_level,omitempty"`
	BuildID      string   `json:"build_id,omitempty"`
	ABIs         []string `json:"abis,omitempty"`
	CurrentUser  int      `json:"current_user"`
	Warnings     []string `json:"warnings,omitempty"`
}

type ADBPackage struct {
	PackageID     string `json:"package_id"`
	ThirdParty    bool   `json:"third_party"`
	System        bool   `json:"system"`
	Enabled       *bool  `json:"enabled,omitempty"`
	VersionCode   string `json:"version_code,omitempty"`
	VersionName   string `json:"version_name,omitempty"`
	TVLaunchable  bool   `json:"tv_launchable"`
	Component     string `json:"component,omitempty"`
}

type ADBPackageInventory struct {
	CurrentUser int          `json:"current_user"`
	Packages    []ADBPackage `json:"packages"`
	Warnings    []string     `json:"warnings,omitempty"`
	Truncated   bool         `json:"truncated,omitempty"`
}

type ADBLaunchable struct {
	PackageID string `json:"package_id"`
	Component string `json:"component"`
}

type ADBLaunchableInventory struct {
	CurrentUser int             `json:"current_user"`
	Launchables []ADBLaunchable `json:"launchables"`
	Warnings    []string        `json:"warnings,omitempty"`
	Truncated   bool            `json:"truncated,omitempty"`
}

var (
	adbPackageIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*$`)
	adbComponentPattern = regexp.MustCompile(`^[A-Za-z0-9_.$]+/[A-Za-z0-9_.$]+$`)
)

func boundedField(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func appendWarning(warnings []string, warning string) []string {
	for _, existing := range warnings {
		if existing == warning {
			return warnings
		}
	}
	return append(warnings, warning)
}

func (m *ADBManager) deviceCommand(ctx context.Context, serial string, args ...string) (ADBResult, error) {
	result, err := m.runDevice(ctx, serial, args...)
	if mapped := mapADBOperationError(result, err); mapped != nil {
		return result, mapped
	}
	return result, nil
}

func (m *ADBManager) currentUser(ctx context.Context, serial string) (int, error) {
	result, err := m.deviceCommand(ctx, serial, "shell", "am", "get-current-user")
	if err != nil {
		return 0, err
	}
	user, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil || user < 0 || user > 100000 {
		return 0, &ADBError{Code: "malformed_output", Message: "ADB returned an invalid current user"}
	}
	return user, nil
}

func (m *ADBManager) DeviceInfo(ctx context.Context, serial string) (ADBDeviceInfo, error) {
	props := []struct {
		key string
		set func(*ADBDeviceInfo, string)
	}{
		{"ro.product.manufacturer", func(v *ADBDeviceInfo, s string) { v.Manufacturer = boundedField(s, 200) }},
		{"ro.product.model", func(v *ADBDeviceInfo, s string) { v.Model = boundedField(s, 200) }},
		{"ro.product.name", func(v *ADBDeviceInfo, s string) { v.Product = boundedField(s, 200) }},
		{"ro.build.version.release", func(v *ADBDeviceInfo, s string) { v.Android = boundedField(s, 100) }},
		{"ro.build.version.sdk", func(v *ADBDeviceInfo, s string) {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 1 && n <= 1000 {
				v.APILevel = n
			}
		}},
		{"ro.build.display.id", func(v *ADBDeviceInfo, s string) { v.BuildID = boundedField(s, 250) }},
		{"ro.product.cpu.abilist", func(v *ADBDeviceInfo, s string) {
			for _, abi := range strings.Split(s, ",") {
				abi = boundedField(abi, 80)
				if abi != "" && !strings.ContainsAny(abi, " \t\r\n") {
					v.ABIs = append(v.ABIs, abi)
				}
			}
		}},
	}
	var out ADBDeviceInfo
	for _, prop := range props {
		result, err := m.deviceCommand(ctx, serial, "shell", "getprop", prop.key)
		if err != nil {
			out.Warnings = appendWarning(out.Warnings, "Some device properties were unavailable.")
			continue
		}
		if result.Truncated {
			out.Warnings = appendWarning(out.Warnings, "A device-property response was truncated.")
		}
		prop.set(&out, result.Stdout)
	}
	user, err := m.currentUser(ctx, serial)
	if err != nil {
		return ADBDeviceInfo{}, err
	}
	out.CurrentUser = user
	sort.Strings(out.ABIs)
	return out, nil
}

func parsePackageLines(output string, max int) (map[string]string, int, bool) {
	result := make(map[string]string)
	malformed := 0
	truncated := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "package:") {
			malformed++
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "package:"))
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			malformed++
			continue
		}
		pkg := fields[0]
		if !adbPackageIDPattern.MatchString(pkg) || len(pkg) > 255 {
			malformed++
			continue
		}
		version := ""
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "versionCode:") {
				version = boundedField(strings.TrimPrefix(field, "versionCode:"), 80)
			}
		}
		if _, exists := result[pkg]; !exists {
			result[pkg] = version
			if len(result) >= max {
				truncated = true
				break
			}
		}
	}
	return result, malformed, truncated
}

func parsePackageSet(output string, max int) (map[string]bool, int, bool) {
	parsed, malformed, truncated := parsePackageLines(output, max)
	out := make(map[string]bool, len(parsed))
	for pkg := range parsed {
		out[pkg] = true
	}
	return out, malformed, truncated
}

func parseLeanbackComponents(output string, max int) ([]ADBLaunchable, int, bool) {
	seen := make(map[string]bool)
	out := make([]ADBLaunchable, 0)
	malformed := 0
	truncated := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "No activities") {
			continue
		}
		fields := strings.Fields(line)
		component := ""
		for _, field := range fields {
			field = strings.Trim(field, "{}[],")
			if adbComponentPattern.MatchString(field) {
				component = field
				break
			}
		}
		if component == "" {
			malformed++
			continue
		}
		parts := strings.SplitN(component, "/", 2)
		pkg := parts[0]
		if !adbPackageIDPattern.MatchString(pkg) || len(pkg) > 255 || len(component) > 500 {
			malformed++
			continue
		}
		if seen[component] {
			continue
		}
		seen[component] = true
		out = append(out, ADBLaunchable{PackageID: pkg, Component: component})
		if len(out) >= max {
			truncated = true
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PackageID == out[j].PackageID {
			return out[i].Component < out[j].Component
		}
		return out[i].PackageID < out[j].PackageID
	})
	return out, malformed, truncated
}

func (m *ADBManager) LeanbackLaunchables(ctx context.Context, serial string, user int) (ADBLaunchableInventory, error) {
	args := []string{"shell", "cmd", "package", "query-activities", "--brief", "--user", strconv.Itoa(user),
		"-a", "android.intent.action.MAIN", "-c", "android.intent.category.LEANBACK_LAUNCHER"}
	result, err := m.deviceCommand(ctx, serial, args...)
	if err != nil {
		return ADBLaunchableInventory{}, err
	}
	items, malformed, capped := parseLeanbackComponents(result.Stdout, maxADBLaunchableRecords)
	out := ADBLaunchableInventory{CurrentUser: user, Launchables: items, Truncated: result.Truncated || capped}
	if malformed > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Ignored %d malformed launcher-query lines.", malformed))
	}
	if out.Truncated {
		out.Warnings = appendWarning(out.Warnings, "Launcher inventory was truncated to its safety limit.")
	}
	return out, nil
}

func (m *ADBManager) PackageInventory(ctx context.Context, serial string) (ADBPackageInventory, error) {
	user, err := m.currentUser(ctx, serial)
	if err != nil {
		return ADBPackageInventory{}, err
	}
	warnings := []string{}

	base, err := m.deviceCommand(ctx, serial, "shell", "pm", "list", "packages", "--user", strconv.Itoa(user), "--show-versioncode")
	if err != nil {
		base, err = m.deviceCommand(ctx, serial, "shell", "pm", "list", "packages", "--user", strconv.Itoa(user))
		if err != nil {
			return ADBPackageInventory{}, err
		}
		warnings = appendWarning(warnings, "Package version codes are unavailable on this Android build.")
	}
	packages, malformed, capped := parsePackageLines(base.Stdout, maxADBPackageRecords)
	if malformed > 0 {
		warnings = append(warnings, fmt.Sprintf("Ignored %d malformed package-list lines.", malformed))
	}

	thirdParty := map[string]bool{}
	thirdKnown := false
	if result, e := m.deviceCommand(ctx, serial, "shell", "pm", "list", "packages", "-3", "--user", strconv.Itoa(user)); e == nil {
		thirdParty, _, _ = parsePackageSet(result.Stdout, maxADBPackageRecords)
		thirdKnown = true
	} else {
		warnings = appendWarning(warnings, "Third-party/system classification is partially unavailable.")
	}

	disabled := map[string]bool{}
	enabledKnown := false
	if result, e := m.deviceCommand(ctx, serial, "shell", "pm", "list", "packages", "-d", "--user", strconv.Itoa(user)); e == nil {
		disabled, _, _ = parsePackageSet(result.Stdout, maxADBPackageRecords)
		enabledKnown = true
	} else {
		warnings = appendWarning(warnings, "Package enabled state is unavailable.")
	}

	launchableByPackage := map[string]string{}
	launchables, launchErr := m.LeanbackLaunchables(ctx, serial, user)
	if launchErr != nil {
		warnings = appendWarning(warnings, "Leanback launcher inventory is unavailable.")
	} else {
		for _, item := range launchables.Launchables {
			if _, exists := launchableByPackage[item.PackageID]; !exists {
				launchableByPackage[item.PackageID] = item.Component
			}
		}
		for _, warning := range launchables.Warnings {
			warnings = appendWarning(warnings, warning)
		}
	}

	ids := make([]string, 0, len(packages))
	for pkg := range packages {
		ids = append(ids, pkg)
	}
	sort.Strings(ids)
	out := ADBPackageInventory{
		CurrentUser: user,
		Packages:    make([]ADBPackage, 0, len(ids)),
		Warnings:    warnings,
		Truncated:   base.Truncated || capped,
	}
	for _, pkg := range ids {
		record := ADBPackage{
			PackageID:    pkg,
			VersionCode:  packages[pkg],
			TVLaunchable: launchableByPackage[pkg] != "",
			Component:    launchableByPackage[pkg],
		}
		if thirdKnown {
			record.ThirdParty = thirdParty[pkg]
			record.System = !record.ThirdParty
		}
		if enabledKnown {
			value := !disabled[pkg]
			record.Enabled = &value
		}
		out.Packages = append(out.Packages, record)
	}
	if out.Truncated {
		out.Warnings = appendWarning(out.Warnings, "Package inventory was truncated to its safety limit.")
	}
	return out, nil
}

func (s *Server) adbTargetSerial(id string) (string, error) {
	tv, err := s.copyTV(id)
	if err != nil {
		return "", err
	}
	if tv.ADBSerial == "" {
		return "", &ADBError{Code: "invalid_target", Message: "This TV has no stored ADB device serial"}
	}
	return tv.ADBSerial, nil
}

func (s *Server) adbDeviceInfo(ctx context.Context, id string) (map[string]any, error) {
	serial, err := s.adbTargetSerial(id)
	if err != nil {
		return nil, err
	}
	info, err := s.adb.DeviceInfo(ctx, serial)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tv_id": id, "device": info}, nil
}

func (s *Server) adbPackages(ctx context.Context, id string) (map[string]any, error) {
	serial, err := s.adbTargetSerial(id)
	if err != nil {
		return nil, err
	}
	inventory, err := s.adb.PackageInventory(ctx, serial)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tv_id": id, "inventory": inventory}, nil
}

func (s *Server) adbLaunchables(ctx context.Context, id string) (map[string]any, error) {
	serial, err := s.adbTargetSerial(id)
	if err != nil {
		return nil, err
	}
	user, err := s.adb.currentUser(ctx, serial)
	if err != nil {
		return nil, err
	}
	inventory, err := s.adb.LeanbackLaunchables(ctx, serial, user)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tv_id": id, "inventory": inventory}, nil
}

func (s *Server) handleADBDeviceInfo(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	result, err := s.adbDeviceInfo(r.Context(), r.PathValue("tv_id"))
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, 200, result)
}

func (s *Server) handleADBPackages(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	result, err := s.adbPackages(r.Context(), r.PathValue("tv_id"))
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, 200, result)
}

func (s *Server) handleADBLaunchables(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	result, err := s.adbLaunchables(r.Context(), r.PathValue("tv_id"))
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, 200, result)
}

var _ = errors.New
