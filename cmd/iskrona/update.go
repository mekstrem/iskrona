package main

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var updateVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func latestUpdate() string {
	return lookupUpdate(&http.Client{Timeout: 3 * time.Second}, "https://api.github.com/repos/mekstrem/iskrona/releases/latest", version)
}

func lookupUpdate(client *http.Client, url, current string) string {
	currentParts := updateVersionPattern.FindStringSubmatch(current)
	if !validUpdateVersion(currentParts) {
		return ""
	}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release) != nil || release.Prerelease || release.Draft {
		return ""
	}
	latest := updateVersionPattern.FindStringSubmatch(release.TagName)
	if !validUpdateVersion(latest) || latest[4] != "" {
		return ""
	}
	for i := 1; i <= 3; i++ {
		if len(latest[i]) != len(currentParts[i]) {
			if len(latest[i]) > len(currentParts[i]) {
				return strings.TrimPrefix(release.TagName, "v")
			}
			return ""
		}
		if latest[i] != currentParts[i] {
			if latest[i] > currentParts[i] {
				return strings.TrimPrefix(release.TagName, "v")
			}
			return ""
		}
	}
	if currentParts[4] != "" {
		return strings.TrimPrefix(release.TagName, "v")
	}
	return ""
}

func validUpdateVersion(parts []string) bool {
	if parts == nil {
		return false
	}
	for _, identifier := range strings.Split(parts[5], ".") {
		if len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "" {
			return false
		}
	}
	return true
}
