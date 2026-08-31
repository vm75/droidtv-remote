package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const maxADBDevices = 512

type ADBDevice struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
}

type ADBPairResult struct {
	Endpoint string `json:"endpoint"`
	GUID     string `json:"guid,omitempty"`
}

type ADBConnectResult struct {
	Endpoint string `json:"endpoint"`
	Serial   string `json:"serial"`
}

var adbPairGUIDPattern = regexp.MustCompile(`(?i)guid=([A-Za-z0-9._:-]{1,255})`)

func validateADBEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || len(endpoint) > 300 || strings.ContainsAny(endpoint, "\r\n\t ") {
		return "", &ADBError{Code: "invalid_endpoint", Message: "A valid explicit ADB host:port endpoint is required"}
	}
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" {
		return "", &ADBError{Code: "invalid_endpoint", Message: "A valid explicit ADB host:port endpoint is required"}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", &ADBError{Code: "invalid_endpoint", Message: "ADB endpoint port must be between 1 and 65535"}
	}
	if strings.ContainsAny(host, "\r\n\t ") {
		return "", &ADBError{Code: "invalid_endpoint", Message: "A valid explicit ADB host:port endpoint is required"}
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func validateADBPairCode(code string) error {
	if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
		return &ADBError{Code: "invalid_pairing_code", Message: "ADB pairing code must be exactly six digits"}
	}
	return nil
}

func adbResultText(result ADBResult) string {
	return strings.ToLower(strings.TrimSpace(result.Stdout + "\n" + result.Stderr))
}

func mapADBOperationError(result ADBResult, err error) error {
	text := adbResultText(result)
	switch {
	case strings.Contains(text, "failed to authenticate"),
		strings.Contains(text, "unauthorized"):
		return &ADBError{Code: "unauthorized_device", Message: "The TV has not authorized this ADB host"}
	case strings.Contains(text, "offline"),
		strings.Contains(text, "unable to connect"),
		strings.Contains(text, "connection refused"),
		strings.Contains(text, "no route to host"),
		strings.Contains(text, "cannot connect"):
		return &ADBError{Code: "offline", Message: "The TV is offline or unreachable over ADB"}
	}
	if err != nil {
		return err
	}
	return nil
}

func (m *ADBManager) Devices(ctx context.Context) ([]ADBDevice, error) {
	if err := m.ensureServer(ctx); err != nil {
		return nil, err
	}
	result, err := m.runHost(ctx, "devices")
	if err != nil {
		if mapped := mapADBOperationError(result, err); mapped != nil {
			return nil, mapped
		}
	}
	lines := strings.Split(result.Stdout, "\n")
	out := make([]ADBDevice, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] == "List" {
			continue
		}
		serial, state := fields[0], fields[1]
		if len(serial) > 300 || strings.HasPrefix(serial, "-") || strings.ContainsAny(serial, "\r\n\t ") {
			continue
		}
		switch state {
		case "device", "unauthorized", "offline":
		default:
			continue
		}
		out = append(out, ADBDevice{Serial: serial, State: state})
		if len(out) >= maxADBDevices {
			break
		}
	}
	return out, nil
}

func (m *ADBManager) Pair(ctx context.Context, endpoint, code string) (ADBPairResult, error) {
	endpoint, err := validateADBEndpoint(endpoint)
	if err != nil {
		return ADBPairResult{}, err
	}
	if err := validateADBPairCode(code); err != nil {
		return ADBPairResult{}, err
	}
	if err := m.ensureServer(ctx); err != nil {
		return ADBPairResult{}, err
	}
	result, runErr := m.runHost(ctx, "pair", endpoint, code)
	if mapped := mapADBOperationError(result, runErr); mapped != nil {
		return ADBPairResult{}, mapped
	}
	text := adbResultText(result)
	if !strings.Contains(text, "successfully paired") {
		return ADBPairResult{}, &ADBError{Code: "pair_failed", Message: "ADB pairing was not accepted by the TV"}
	}
	out := ADBPairResult{Endpoint: endpoint}
	if match := adbPairGUIDPattern.FindStringSubmatch(result.Stdout + "\n" + result.Stderr); len(match) == 2 {
		out.GUID = match[1]
	}
	return out, nil
}

func (m *ADBManager) Connect(ctx context.Context, endpoint string) (ADBConnectResult, error) {
	endpoint, err := validateADBEndpoint(endpoint)
	if err != nil {
		return ADBConnectResult{}, err
	}
	if err := m.ensureServer(ctx); err != nil {
		return ADBConnectResult{}, err
	}
	result, runErr := m.runHost(ctx, "connect", endpoint)
	if mapped := mapADBOperationError(result, runErr); mapped != nil {
		return ADBConnectResult{}, mapped
	}
	text := adbResultText(result)
	if !strings.Contains(text, "connected to") && !strings.Contains(text, "already connected to") {
		return ADBConnectResult{}, &ADBError{Code: "connect_failed", Message: "ADB did not confirm the connection"}
	}
	return ADBConnectResult{Endpoint: endpoint, Serial: endpoint}, nil
}

func (m *ADBManager) Disconnect(ctx context.Context, serial string) error {
	serial = strings.TrimSpace(serial)
	if serial == "" || len(serial) > 300 || strings.HasPrefix(serial, "-") || strings.ContainsAny(serial, "\r\n\t ") {
		return &ADBError{Code: "invalid_target", Message: "An explicit stored ADB device serial is required"}
	}
	if err := m.ensureServer(ctx); err != nil {
		return err
	}
	result, runErr := m.runHost(ctx, "disconnect", serial)
	return mapADBOperationError(result, runErr)
}

func (s *Server) adbAuthorized(r *http.Request) bool {
	if s.adbAdminToken == "" {
		return false
	}
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if len(provided) != len(s.adbAdminToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.adbAdminToken)) == 1
}

func (s *Server) requireADBAuthorization(r *http.Request) error {
	if !s.adbAuthorized(r) {
		return &ADBError{Code: "unauthorized", Message: "ADB administrator authorization required"}
	}
	return nil
}

func adbNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func writeADBError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "ADB operation failed"
	var adbErr *ADBError
	if errors.As(err, &adbErr) {
		code = adbErr.Code
		message = adbErr.Error()
		switch adbErr.Code {
		case "unauthorized":
			status = http.StatusUnauthorized
		case "invalid_endpoint", "invalid_pairing_code", "invalid_target":
			status = http.StatusBadRequest
		case "disabled", "unavailable", "missing_admin_token":
			status = http.StatusServiceUnavailable
		case "unsupported_command":
			status = http.StatusNotImplemented
		case "malformed_output":
			status = http.StatusBadGateway
		case "timeout":
			status = http.StatusGatewayTimeout
		case "unauthorized_device", "offline", "pair_failed", "connect_failed":
			status = http.StatusConflict
		default:
			status = http.StatusBadGateway
		}
	} else if err != nil && err.Error() == "Unknown TV" {
		status = http.StatusNotFound
		code = "unknown_tv"
		message = "Unknown TV"
	}
	jsonResponse(w, status, map[string]any{"error": message, "code": code})
}

func (s *Server) copyTV(id string) (TV, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.tvs[id]
	if t == nil {
		return TV{}, errors.New("Unknown TV")
	}
	out := *t
	out.AppIDs = append([]string(nil), t.AppIDs...)
	return out, nil
}

func (s *Server) setADBState(id, state string) {
	st := s.state(id)
	st.mu.Lock()
	st.adbState = state
	st.mu.Unlock()
}

func (s *Server) transientADBState(id string) string {
	st := s.state(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.adbState == "pairing" || st.adbState == "connecting" {
		return st.adbState
	}
	return ""
}

func (s *Server) updateADBAssociation(id string, serial, endpoint, pairGUID *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tvs[id]
	if t == nil {
		return errors.New("Unknown TV")
	}
	if serial != nil {
		t.ADBSerial = *serial
	}
	if endpoint != nil {
		t.ADBEndpoint = *endpoint
	}
	if pairGUID != nil {
		t.ADBPairGUID = *pairGUID
	}
	return s.saveTVsLocked()
}

func (s *Server) adbState(ctx context.Context, id string) (map[string]any, error) {
	tv, err := s.copyTV(id)
	if err != nil {
		return nil, err
	}
	availability := s.adb.Availability(ctx)
	out := map[string]any{
		"state":     "disabled",
		"enabled":   availability.Enabled,
		"available": availability.Available,
		"serial":    nullable(tv.ADBSerial),
		"endpoint":  nullable(tv.ADBEndpoint),
		"pair_guid": nullable(tv.ADBPairGUID),
		"paired":    tv.ADBSerial != "" || tv.ADBPairGUID != "",
	}
	if availability.Version != "" {
		out["version"] = availability.Version
	}
	if !availability.Enabled {
		return out, nil
	}
	if !availability.Available {
		out["state"] = "unavailable"
		if availability.Error != "" {
			out["error"] = availability.Error
		}
		return out, nil
	}
	if transient := s.transientADBState(id); transient != "" {
		out["state"] = transient
		return out, nil
	}
	if tv.ADBSerial == "" {
		if tv.ADBPairGUID != "" {
			out["state"] = "offline"
		} else {
			out["state"] = "unpaired"
		}
		return out, nil
	}
	devices, err := s.adb.Devices(ctx)
	if err != nil {
		var adbErr *ADBError
		if errors.As(err, &adbErr) {
			out["state"] = "unavailable"
			out["error"] = adbErr.Code
			return out, nil
		}
		return nil, err
	}
	var matched *ADBDevice
	for i := range devices {
		if devices[i].Serial == tv.ADBSerial {
			matched = &devices[i]
			break
		}
	}
	if matched == nil && tv.ADBPairGUID != "" {
		for i := range devices {
			if strings.Contains(devices[i].Serial, tv.ADBPairGUID) {
				matched = &devices[i]
				serial := devices[i].Serial
				if err := s.updateADBAssociation(id, &serial, nil, nil); err == nil {
					out["serial"] = serial
				}
				break
			}
		}
	}
	if matched == nil {
		out["state"] = "offline"
		return out, nil
	}
	switch matched.State {
	case "device":
		out["state"] = "connected"
	case "unauthorized":
		out["state"] = "unauthorized"
	default:
		out["state"] = "offline"
	}
	return out, nil
}

func (s *Server) adbStatusResult(ctx context.Context, id string) (map[string]any, error) {
	tv, err := s.copyTV(id)
	if err != nil {
		return nil, err
	}
	adb, err := s.adbState(ctx, id)
	if err != nil {
		return nil, err
	}
	remoteStatus := s.tvStatus(id)
	remote := map[string]any{
		"connected":           remoteStatus["connected"],
		"connecting":          remoteStatus["connecting"],
		"pairing_in_progress": remoteStatus["pairing_in_progress"],
	}
	return map[string]any{
		"tv_id":   tv.ID,
		"tv_name": tv.Name,
		"remote":  remote,
		"adb":     adb,
	}, nil
}

func (s *Server) adbPair(ctx context.Context, id, endpoint, code string) (map[string]any, error) {
	if _, err := s.copyTV(id); err != nil {
		return nil, err
	}
	s.setADBState(id, "pairing")
	defer s.setADBState(id, "")
	result, err := s.adb.Pair(ctx, endpoint, code)
	if err != nil {
		return nil, err
	}
	guid := result.GUID
	if err := s.updateADBAssociation(id, nil, nil, &guid); err != nil {
		return nil, err
	}
	return map[string]any{
		"tv_id":     id,
		"state":     "offline",
		"paired":    true,
		"pair_guid": nullable(guid),
		"endpoint":  result.Endpoint,
	}, nil
}

func (s *Server) adbConnect(ctx context.Context, id, endpoint string) (map[string]any, error) {
	if _, err := s.copyTV(id); err != nil {
		return nil, err
	}
	s.setADBState(id, "connecting")
	result, err := s.adb.Connect(ctx, endpoint)
	if err != nil {
		s.setADBState(id, "")
		return nil, err
	}
	serial, target := result.Serial, result.Endpoint
	if err := s.updateADBAssociation(id, &serial, &target, nil); err != nil {
		s.setADBState(id, "")
		return nil, err
	}
	s.setADBState(id, "")
	state, err := s.adbState(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tv_id": id, "adb": state}, nil
}

func (s *Server) adbDisconnect(ctx context.Context, id string) (map[string]any, error) {
	tv, err := s.copyTV(id)
	if err != nil {
		return nil, err
	}
	if tv.ADBSerial == "" {
		return nil, &ADBError{Code: "invalid_target", Message: "This TV has no stored ADB device serial"}
	}
	if err := s.adb.Disconnect(ctx, tv.ADBSerial); err != nil {
		return nil, err
	}
	s.setADBState(id, "")
	state, err := s.adbState(ctx, id)
	if err != nil {
		return nil, err
	}
	state["state"] = "offline"
	return map[string]any{"tv_id": id, "adb": state}, nil
}

func (s *Server) adbForget(id string) (map[string]any, error) {
	if _, err := s.copyTV(id); err != nil {
		return nil, err
	}
	empty := ""
	if err := s.updateADBAssociation(id, &empty, &empty, &empty); err != nil {
		return nil, err
	}
	s.setADBState(id, "")
	return map[string]any{
		"tv_id":   id,
		"status":  "forgotten",
		"state":   "unpaired",
		"warning": "The shared ADB host key is not revoked on the TV; revoke or forget it in the TV debugging settings if required.",
	}, nil
}

func (s *Server) handleADBStatus(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	result, err := s.adbStatusResult(r.Context(), r.PathValue("tv_id"))
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) handleADBPair(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Code     string `json:"code"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeADBError(w, &ADBError{Code: "invalid_pairing_code", Message: "Invalid ADB pairing request"})
		return
	}
	result, err := s.adbPair(r.Context(), r.PathValue("tv_id"), in.Endpoint, in.Code)
	in.Code = ""
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) handleADBConnect(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeADBError(w, &ADBError{Code: "invalid_endpoint", Message: "Invalid ADB connect request"})
		return
	}
	result, err := s.adbConnect(r.Context(), r.PathValue("tv_id"), in.Endpoint)
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) handleADBDisconnect(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	result, err := s.adbDisconnect(r.Context(), r.PathValue("tv_id"))
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) handleADBForget(w http.ResponseWriter, r *http.Request) {
	adbNoStore(w)
	if err := s.requireADBAuthorization(r); err != nil {
		writeADBError(w, err)
		return
	}
	result, err := s.adbForget(r.PathValue("tv_id"))
	if err != nil {
		writeADBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

func adbErrorCode(err error) string {
	var adbErr *ADBError
	if errors.As(err, &adbErr) {
		return adbErr.Code
	}
	if err != nil && err.Error() == "Unknown TV" {
		return "unknown_tv"
	}
	return "internal_error"
}

func adbStructuredError(err error) map[string]any {
	return map[string]any{"code": adbErrorCode(err), "message": fmt.Sprint(err)}
}
