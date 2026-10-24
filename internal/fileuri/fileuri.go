// Package fileuri converts local filesystem paths to file URLs and back without
// treating a Windows drive letter as a URL authority or a rooted relative path.
package fileuri

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// FromPath returns an absolute file URL suitable for ResolveReference.
func FromPath(name string) (*url.URL, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	return fromAbsolutePath(absolute, runtime.GOOS), nil
}

func fromAbsolutePath(name, goos string) *url.URL {
	if goos == "windows" {
		name = strings.ReplaceAll(name, `\`, "/")
		if strings.HasPrefix(name, "//") {
			host, rest, _ := strings.Cut(name[2:], "/")
			return &url.URL{Scheme: "file", Host: host, Path: "/" + rest}
		}
		// The slash is part of the URI syntax, not the native drive path.
		name = "/" + name
	}
	return &url.URL{Scheme: "file", Path: name}
}

// Path converts a file URL to a native absolute path. URL.Path is already
// percent-decoded by net/url; decoding it again would corrupt literal percent
// sequences in contract filenames.
func Path(uri *url.URL) (string, error) {
	return pathForOS(uri, runtime.GOOS)
}

func pathForOS(uri *url.URL, goos string) (string, error) {
	if uri == nil || uri.Scheme != "file" || uri.Opaque != "" || uri.User != nil {
		return "", fmt.Errorf("expected an absolute file URL")
	}
	name := uri.Path
	host := uri.Host
	if strings.EqualFold(host, "localhost") {
		host = ""
	}
	if goos == "windows" {
		if host != "" {
			if strings.ContainsAny(host, `:/\`) || !strings.HasPrefix(name, "/") || len(name) <= 1 {
				return "", fmt.Errorf("invalid UNC file URL %q", uri)
			}
			return `\\` + host + strings.ReplaceAll(name, "/", `\`), nil
		}
		if len(name) >= 3 && name[0] == '/' && driveLetter(name[1]) && name[2] == ':' {
			name = name[1:]
		}
		if !(len(name) >= 3 && driveLetter(name[0]) && name[1] == ':' && name[2] == '/') && !strings.HasPrefix(name, "//") {
			return "", fmt.Errorf("file URL has no absolute Windows drive or UNC path: %q", uri)
		}
		return strings.ReplaceAll(name, "/", `\`), nil
	}
	if host != "" {
		return "", fmt.Errorf("remote file URL authority %q is unsupported on %s", host, goos)
	}
	if !strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("file URL has no absolute path: %q", uri)
	}
	return name, nil
}

func driveLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
