package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	ServerPort int
	TVIP       string
	TVName     string
	LegacyApps []App
}

type App struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PackageID string `json:"package_id"`
	Icon      string `json:"-"`
	IconFile  string `json:"-"`
}

type AppJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PackageID       string `json:"package_id"`
	Icon            string `json:"icon"`
	IconClass       string `json:"icon_class"`
	HasUploadedIcon bool   `json:"has_uploaded_icon"`
}

type TV struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	AppIDs      []string `json:"app_ids"`
	ADBSerial   string   `json:"adb_serial,omitempty"`
	ADBEndpoint string   `json:"adb_endpoint,omitempty"`
	ADBPairGUID string   `json:"adb_pair_guid,omitempty"`
}

func yamlValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	if s[0] == '"' && s[len(s)-1] == '"' {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func splitYAML(line string) (string, string, bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), yamlValue(line[i+1:]), true
}

func loadConfig(path string) Config {
	cfg := Config{ServerPort: 7503}
	f, err := os.Open(path)
	if err != nil {
		return cfg
	}
	defer f.Close()
	var current *App
	inApps := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		raw := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if indent == 0 && strings.HasPrefix(trimmed, "apps:") {
			inApps = true
			continue
		}
		if indent == 0 && !strings.HasPrefix(trimmed, "-") {
			inApps = false
		}
		if inApps {
			if strings.HasPrefix(trimmed, "-") {
				cfg.LegacyApps = append(cfg.LegacyApps, App{})
				current = &cfg.LegacyApps[len(cfg.LegacyApps)-1]
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
				if trimmed == "" {
					continue
				}
			}
			if current != nil {
				k, v, ok := splitYAML(trimmed)
				if !ok {
					continue
				}
				switch k {
				case "name":
					current.Name = v
				case "id", "package_id":
					current.PackageID = v
				case "icon":
					current.Icon = v
				}
			}
			continue
		}
		k, v, ok := splitYAML(trimmed)
		if !ok {
			continue
		}
		switch k {
		case "server_port":
			if n, e := strconv.Atoi(v); e == nil {
				cfg.ServerPort = n
			}
		case "tv_ip":
			cfg.TVIP = v
		case "tv_name":
			cfg.TVName = v
		}
	}
	return cfg
}

func parseListMaps(path, root string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var result []map[string]any
	var current map[string]any
	currentList := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		raw := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == root+":" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if strings.Contains(rest, ":") {
				current = map[string]any{}
				result = append(result, current)
				currentList = ""
				k, v, _ := splitYAML(rest)
				current[k] = v
				continue
			}
			if current != nil && currentList != "" {
				list, _ := current[currentList].([]string)
				list = append(list, yamlValue(rest))
				current[currentList] = list
			}
			continue
		}
		if current == nil {
			continue
		}
		k, v, ok := splitYAML(trimmed)
		if !ok {
			continue
		}
		if k == "app_ids" && v == "[]" {
			current[k] = []string{}
			currentList = ""
		} else if v == "" && k == "app_ids" {
			current[k] = []string{}
			currentList = k
		} else {
			current[k] = v
			currentList = ""
		}
	}
	return result, sc.Err()
}

func quoteYAML(s string) string { return strconv.Quote(s) }

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func saveApps(path string, apps []*App, order []string) error {
	var b strings.Builder
	b.WriteString("apps:\n")
	for _, id := range order {
		a := findApp(apps, id)
		if a == nil {
			continue
		}
		fmt.Fprintf(&b, "- id: %s\n  name: %s\n  package_id: %s\n  icon: %s\n  icon_file: %s\n", quoteYAML(a.ID), quoteYAML(a.Name), quoteYAML(a.PackageID), quoteYAML(a.Icon), quoteYAML(a.IconFile))
	}
	return writeAtomic(path, []byte(b.String()))
}

func saveTVs(path string, tvs []*TV, order []string) error {
	var b strings.Builder
	b.WriteString("tvs:\n")
	for _, id := range order {
		t := findTV(tvs, id)
		if t == nil {
			continue
		}
		fmt.Fprintf(&b, "- id: %s\n  name: %s\n  host: %s\n", quoteYAML(t.ID), quoteYAML(t.Name), quoteYAML(t.Host))
		if t.ADBSerial != "" {
			fmt.Fprintf(&b, "  adb_serial: %s\n", quoteYAML(t.ADBSerial))
		}
		if t.ADBEndpoint != "" {
			fmt.Fprintf(&b, "  adb_endpoint: %s\n", quoteYAML(t.ADBEndpoint))
		}
		if t.ADBPairGUID != "" {
			fmt.Fprintf(&b, "  adb_pair_guid: %s\n", quoteYAML(t.ADBPairGUID))
		}
		b.WriteString("  app_ids:\n")
		for _, aid := range t.AppIDs {
			fmt.Fprintf(&b, "  - %s\n", quoteYAML(aid))
		}
	}
	return writeAtomic(path, []byte(b.String()))
}

func findApp(apps []*App, id string) *App {
	for _, a := range apps {
		if a.ID == id {
			return a
		}
	}
	return nil
}
func findTV(tvs []*TV, id string) *TV {
	for _, t := range tvs {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func legacyID(seed string) string {
	h := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(h[:])[:16]
}
