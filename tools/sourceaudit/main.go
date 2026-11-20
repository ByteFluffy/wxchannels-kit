// Command sourceaudit checks source distributions, not private runtime folders.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	rootFlag := flag.String("root", ".", "Source repository root")
	flag.Parse()
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fail(err)
	}
	privateKey := regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`)
	var violations []string
	files, vendorFiles := 0, 0
	var vendorBytes int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || rel == "dist" || rel == "runtime" || rel == ".gocache" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			violations = append(violations, rel+": source symlink requires explicit audit")
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files++
		inVendor := strings.HasPrefix(filepath.ToSlash(rel), "vendor/")
		if inVendor {
			vendorFiles++
			vendorBytes += info.Size()
		}
		if !inVendor {
			ext := strings.ToLower(filepath.Ext(path))
			switch ext {
			case ".key", ".pem", ".pfx", ".p12", ".cer", ".crt", ".mp4", ".mov", ".mkv", ".sqlite", ".db", ".log":
				violations = append(violations, rel+": private/runtime file in source tree")
			}
			if entry.Name() == "config.yaml" || entry.Name() == "cookies.json" || entry.Name() == ".env" {
				violations = append(violations, rel+": runtime configuration in source tree")
			}
		}
		// Stream even large native/embed assets; do not silently skip a binary
		// just because it is bigger than an arbitrary audit size limit.
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		containsPrivateKey := privateKey.MatchReader(bufio.NewReader(file))
		closeErr := file.Close()
		if closeErr != nil {
			return closeErr
		}
		if containsPrivateKey {
			violations = append(violations, rel+": PEM private key material is forbidden")
		}
		return nil
	})
	if err != nil {
		fail(err)
	}
	if vendorFiles == 0 {
		violations = append(violations, "vendor: dependency source is missing")
	}
	result := struct {
		Files       int      `json:"source_files"`
		VendorFiles int      `json:"vendor_files"`
		VendorBytes int64    `json:"vendor_bytes"`
		Violations  []string `json:"violations"`
	}{files, vendorFiles, vendorBytes, violations}
	if result.Violations == nil {
		result.Violations = []string{}
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err)
	}
	if len(violations) != 0 {
		os.Exit(1)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "source audit failed:", err); os.Exit(1) }
