package websocket

import "strings"

const proxyFileURLPrefix = "/api/v1/files/"

func buildProxyFileURL(fileID string) string {
	return proxyFileURLPrefix + fileID
}

func extractProxyFileID(fileURL string) string {
	if !strings.HasPrefix(fileURL, proxyFileURLPrefix) {
		return ""
	}

	fileID := strings.TrimPrefix(fileURL, proxyFileURLPrefix)
	if strings.Contains(fileID, "?") {
		return ""
	}
	return fileID
}
