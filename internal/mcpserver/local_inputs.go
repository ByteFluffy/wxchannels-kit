package mcpserver

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var shortSharePath = regexp.MustCompile(`^/sph/[A-Za-z0-9_-]+$`)
var shareID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var portableSpec = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
var deviceFilename = regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)

// LocalAPIURL prevents this unauthenticated local control plane from being
// redirected to arbitrary hosts. localhost is normalized to a numeric address.
func LocalAPIURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("invalid local API URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	port := parsed.Port()
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || ip == nil || !ip.IsLoopback() || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || port == "0" || parsed.Opaque != "" {
		return "", errors.New("the API must be a loopback URL without credentials, query, or extra paths")
	}
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", errors.New("invalid local API port")
		}
		parsed.Host = net.JoinHostPort(ip.String(), strconv.Itoa(number))
	} else if ip.To4() == nil {
		parsed.Host = "[" + ip.String() + "]"
	} else {
		parsed.Host = ip.String()
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func normalizeShareURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 8192 || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("provide one WeChat Channels HTTPS share URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return "", errors.New("share URL must use HTTPS without credentials or fragments")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "weixin.qq.com" && shortSharePath.MatchString(parsed.Path) {
		return "https://weixin.qq.com" + parsed.Path, nil
	}
	if host == "channels.weixin.qq.com" {
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			return "", errors.New("invalid share URL query")
		}
		if parsed.Path == "/finder-preview/pages/sph" && len(query["id"]) == 1 && shareID.MatchString(query.Get("id")) {
			return "https://weixin.qq.com/sph/" + query.Get("id"), nil
		}
		if parsed.Path == "/web/pages/feed" && (query.Get("eid") != "" || (query.Get("oid") != "" && query.Get("nid") != "")) {
			parsed.Host = "channels.weixin.qq.com"
			return parsed.String(), nil
		}
	}
	return "", errors.New("unsupported link; use a weixin.qq.com/sph/ or Channels feed share URL")
}

func validatePortableFilename(value string) error {
	if value == "" {
		return nil
	}
	base, _, _ := strings.Cut(value, ".")
	if value != strings.TrimSpace(value) || len(value) > 200 || value == "." || value == ".." || strings.HasSuffix(value, ".") || strings.ContainsAny(value, `<>:"/\|?*`) || strings.IndexFunc(value, unicode.IsControl) >= 0 || deviceFilename.MatchString(strings.ToUpper(base)) {
		return errors.New("filename must be a portable filename, not a path or reserved device name")
	}
	return nil
}

func localOutputDir(value string) (string, error) {
	if value == "" {
		value = "~/Downloads"
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot determine the home directory")
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~"))
	} else if strings.HasPrefix(value, "~") {
		return "", errors.New("use ~/ for home-directory expansion")
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("output directory contains control characters")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("invalid output directory")
	}
	return absolute, nil
}

// containedOutput is called before inspecting a server-returned file, including
// after symlink resolution. It does not move files or bypass the task database.
func containedOutput(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("output directory is unavailable")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("the completed task output is missing")
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("task output is outside the requested directory")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", errors.New("task output is empty or not a regular file")
	}
	return resolved, nil
}
