package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalAPIURLAcceptsOnlyNumericLoopbackAndLocalhost(t *testing.T) {
	for input, expected := range map[string]string{
		"http://localhost:2022/": "http://127.0.0.1:2022",
		"http://127.0.0.1:2022":  "http://127.0.0.1:2022",
		"https://[::1]:2022":     "https://[::1]:2022",
	} {
		got, err := LocalAPIURL(input)
		if err != nil || got != expected {
			t.Errorf("%q -> %q / %v", input, got, err)
		}
	}
	for _, input := range []string{
		"http://example.invalid:2022", "http://0.0.0.0:2022", "http://192.168.1.2:2022",
		"http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:2022/path",
		"http://user:password@127.0.0.1:2022", "http://127.0.0.1:2022?", "http://127.0.0.1:2022?key=secret",
		"file:///tmp/api", "http://localhost.evil.invalid", "http://127.0.0.1:2022#fragment",
	} {
		if value, err := LocalAPIURL(input); err == nil {
			t.Errorf("accepted %q -> %q", input, value)
		}
	}
}

func TestShareURLCanonicalization(t *testing.T) {
	for input, expected := range map[string]string{
		"https://weixin.qq.com/sph/fixture?tracking=1":                       "https://weixin.qq.com/sph/fixture",
		"https://channels.weixin.qq.com/finder-preview/pages/sph?id=fixture": "https://weixin.qq.com/sph/fixture",
		"https://channels.weixin.qq.com/web/pages/feed?oid=one&nid=two":      "https://channels.weixin.qq.com/web/pages/feed?oid=one&nid=two",
	} {
		if actual, err := normalizeShareURL(input); err != nil || actual != expected {
			t.Errorf("%q -> %q / %v", input, actual, err)
		}
	}
	for _, input := range []string{
		"http://weixin.qq.com/sph/fixture", "https://weixin.qq.com.evil.invalid/sph/fixture",
		"https://weixin.qq.com@evil.invalid/sph/fixture", "https://user@weixin.qq.com/sph/fixture",
		"https://weixin.qq.com:444/sph/fixture", "https://weixin.qq.com/sph/fixture#fragment",
		"https://weixin.qq.com/sph/fixture\nheader: secret", "https://channels.weixin.qq.com/web/pages/feed",
		"https://channels.weixin.qq.com/finder-preview/pages/sph?id=one&id=two", strings.Repeat("a", 8193),
	} {
		if actual, err := normalizeShareURL(input); err == nil {
			t.Errorf("accepted %q -> %q", input, actual)
		}
	}
}

func TestPortableFilename(t *testing.T) {
	for _, value := range []string{"", "my-video", "视频.mp4", "a-b_c.720p"} {
		if err := validatePortableFilename(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"../escape", "a/b", "a\\b", "C:escape", "CON.mp4", "nul", "COM1", "LPT9.mp4", "name.", " leading", "trailing ", "bad\nname", strings.Repeat("a", 201)} {
		if err := validatePortableFilename(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestHomeOutputExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"", "~/Downloads"} {
		actual, err := localOutputDir(input)
		if err != nil || actual != filepath.Join(home, "Downloads") {
			t.Errorf("unexpected expansion %q -> %q / %v", input, actual, err)
		}
	}
	if _, err := localOutputDir("~other/Downloads"); err == nil {
		t.Fatal("accepted another user's home expansion")
	}
}
