package main

import (
	"encoding/json"
	"strings"
)

const maxVehiclePhotos = 10

func parseVehiclePhotoURLsJSON(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return []string{}
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, u := range arr {
		u = strings.TrimSpace(u)
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

func marshalVehiclePhotoURLs(urls []string) (string, error) {
	if len(urls) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(urls)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// effectiveVehiclePhotoList returns stored gallery URLs, or a single legacy cover URL.
func effectiveVehiclePhotoList(photoURLsJSON, legacyPhotoURL string) []string {
	urls := parseVehiclePhotoURLsJSON(photoURLsJSON)
	if len(urls) > 0 {
		return urls
	}
	l := strings.TrimSpace(legacyPhotoURL)
	if l != "" {
		return []string{l}
	}
	return []string{}
}

func primaryVehiclePhoto(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}
