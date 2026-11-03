package keyconfigsync

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

var (
	hexWord = regexp.MustCompile(`^0x[0-9A-Fa-f]{8}$`)
	guid    = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	version = regexp.MustCompile(`^[0-9]{1,3}\.[0-9]{1,3}$`)
	iniName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

func ValidData(b []byte) bool { return ValidProfile(0, b) }
func ValidProfile(slot uint32, b []byte) bool {
	if slot >= SlotCount || len(b) == 0 || len(b) > int(Limit(slot)) || bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	for _, c := range b {
		if c < 32 && c != '\r' && c != '\n' && c != '\t' {
			return false
		}
	}
	if slot == 2 {
		return validIni(b)
	}
	return validKeyconfig(b)
}

// The native format deliberately has unclosed keydata elements and legacy
// encoded names. Parse its small grammar as bytes rather than XML/UTF-8.
func tag(line string) (string, map[string]string, bool) {
	if len(line) < 3 || line[0] != '<' || line[len(line)-1] != '>' {
		return "", nil, false
	}
	line = line[1 : len(line)-1]
	line = strings.TrimSuffix(line, "/")
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, map[string]string{}, true
	}
	name, rest := line[:i], line[i:]
	attrs := make(map[string]string)
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 || eq > 40 {
			return "", nil, false
		}
		key := rest[:eq]
		if !iniName.MatchString(key) || len(rest) < eq+3 || rest[eq+1] != '"' {
			return "", nil, false
		}
		rest = rest[eq+2:]
		end := strings.IndexByte(rest, '"')
		if end < 0 || end > 260 {
			return "", nil, false
		}
		if _, exists := attrs[key]; exists {
			return "", nil, false
		}
		attrs[key] = rest[:end]
		rest = rest[end+1:]
		if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' {
			return "", nil, false
		}
		if len(attrs) > 32 {
			return "", nil, false
		}
	}
	return name, attrs, true
}
func number(s string, max int) bool {
	n, err := strconv.Atoi(s)
	return s != "" && len(s) <= 10 && err == nil && n >= 0 && n <= max
}
func keys(a map[string]string, names ...string) bool {
	if len(a) != len(names) {
		return false
	}
	for _, n := range names {
		if _, ok := a[n]; !ok {
			return false
		}
	}
	return true
}

func validKeyconfig(b []byte) bool {
	root, closed, cat, act, declared := false, false, false, false, false
	var meta [4]bool
	category, nextAction, totalActions, keyCount, comments, blanks := 0, 0, 0, 0, 0, 0
	controllerKeys := strings.Fields("ASSIGN_DLU ASSIGN_DLD ASSIGN_DLL ASSIGN_DLR ASSIGN_DRD ASSIGN_DRR ASSIGN_R2 ASSIGN_L2 ASSIGN_DRL ASSIGN_DRU ASSIGN_R1 ASSIGN_L1 ASSIGN_L3 ASSIGN_R3 ASSIGN_LC ASSIGN_RC ASSIGN_ALU ASSIGN_ALD ASSIGN_ALL ASSIGN_ALR ASSIGN_ARU ASSIGN_ARD ASSIGN_ARL ASSIGN_ARR")
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 1025), 1025)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) > 1024 {
			return false
		}
		line := strings.TrimSpace(string(raw))
		if line == "" {
			blanks++
			if blanks > 32 {
				return false
			}
			continue
		}
		if strings.HasPrefix(line, "<!--") && strings.HasSuffix(line, "-->") {
			comments += len(line)
			if comments > 4096 {
				return false
			}
			continue
		}
		if strings.HasPrefix(line, "<?xml ") && strings.HasSuffix(line, "?>") && !root && !declared && len(line) < 128 {
			declared = true
			continue
		}
		if line == "<keyconfig>" {
			if root {
				return false
			}
			root = true
			continue
		}
		if !root || closed {
			return false
		}
		switch line {
		case "</keyconfig>":
			if cat || act || category == 0 || totalActions == 0 {
				return false
			}
			closed = true
			continue
		case "</category>":
			if !cat || act || nextAction == 0 {
				return false
			}
			cat = false
			continue
		case "</action>":
			if !act || keyCount != 18 {
				return false
			}
			act = false
			continue
		}
		name, a, ok := tag(line)
		if !ok {
			return false
		}
		switch name {
		case "version":
			if meta[0] || cat || !keys(a, "ver") || !version.MatchString(a["ver"]) {
				return false
			}
			meta[0] = true
		case "controller":
			if meta[1] || cat || len(a) != 29 || !guid.MatchString(a["guid"]) || !hexWord.MatchString(a["axes"]) || !number(a["dead_zone"], 90) || !number(a["operate_type"], 16) {
				return false
			}
			if _, ok := a["name"]; !ok {
				return false
			}
			for _, key := range controllerKeys {
				if !hexWord.MatchString(a[key]) {
					return false
				}
			}
			meta[1] = true
		case "keyboard":
			if meta[2] || cat || !keys(a, "operate_type") || !number(a["operate_type"], 16) {
				return false
			}
			meta[2] = true
		case "mouse":
			if meta[3] || cat || !keys(a, "operate_type", "ASSIGN_MOVE", "ASSIGN_LB", "ASSIGN_RB", "ASSIGN_CB", "ASSIGN_WU", "ASSIGN_WD", "ASSIGN_CBWU", "ASSIGN_CBWD") || !number(a["operate_type"], 16) {
				return false
			}
			for k, v := range a {
				if k != "operate_type" && (len(v) > 40 || !iniName.MatchString(v)) {
					return false
				}
			}
			meta[3] = true
		case "category":
			if cat || act || category >= 64 || !meta[0] || !meta[1] || !meta[2] || !meta[3] || !keys(a, "id", "name") || a["id"] != strconv.Itoa(category) {
				return false
			}
			cat = true
			category++
			nextAction = 0
		case "action":
			if !cat || act || nextAction >= 256 || totalActions >= 1024 || !keys(a, "id", "name") || a["id"] != strconv.Itoa(nextAction) {
				return false
			}
			act = true
			nextAction++
			totalActions++
			keyCount = 0
		case "keydata":
			if !act || keyCount >= 18 || !keys(a, "key", "subkey") || !hexWord.MatchString(a["key"]) || !hexWord.MatchString(a["subkey"]) {
				return false
			}
			keyCount++
		default:
			return false
		}
	}
	return scanner.Err() == nil && closed && !cat && !act
}

func validIni(b []byte) bool {
	section := ""
	sections, entries := 0, 0
	known := false
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 1025), 1025)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) > 1024 {
			return false
		}
		line := strings.TrimSpace(string(raw))
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if len(line) < 3 || line[len(line)-1] != ']' || len(line) > 66 {
				return false
			}
			section = strings.ToUpper(line[1 : len(line)-1])
			if !iniName.MatchString(section) {
				return false
			}
			sections++
			if sections > 512 {
				return false
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if section == "" || eq < 1 || eq > 80 {
			return false
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if !iniName.MatchString(key) || len(value) > 512 {
			return false
		}
		entries++
		if entries > 8192 {
			return false
		}
		if section == "OPTION" || section == "SCREEN" || section == "VIDEO" || section == "SOUND" {
			known = true
		}
	}
	return scanner.Err() == nil && known && entries > 0
}
