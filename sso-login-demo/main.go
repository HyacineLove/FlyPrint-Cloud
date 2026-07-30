package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	config, address, err := configurationFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	server, err := newServer(config)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("SSO Login Demo listening on %s", address)
	log.Fatal(http.ListenAndServe(address, server.Handler()))
}

func configurationFromEnvironment() (configuration, string, error) {
	dataFile := strings.TrimSpace(os.Getenv("SSO_DATA_FILE"))
	operatorUsername := strings.TrimSpace(os.Getenv("SSO_OPERATOR_USERNAME"))
	operatorPassword := os.Getenv("SSO_OPERATOR_PASSWORD")
	clientSecret := os.Getenv("SSO_CLIENT_SECRET")
	redirects := splitNonEmpty(os.Getenv("SSO_ALLOWED_REDIRECT_URIS"))
	if dataFile == "" || operatorUsername == "" || operatorPassword == "" || clientSecret == "" || len(redirects) == 0 {
		return configuration{}, "", fmt.Errorf("SSO_DATA_FILE, SSO_OPERATOR_USERNAME, SSO_OPERATOR_PASSWORD, SSO_CLIENT_SECRET, and SSO_ALLOWED_REDIRECT_URIS are required")
	}
	port := 8080
	if raw := strings.TrimSpace(os.Getenv("SSO_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return configuration{}, "", fmt.Errorf("SSO_PORT is invalid")
		}
		port = parsed
	}
	return configuration{
		DataFile:            dataFile,
		OperatorUsername:    operatorUsername,
		OperatorPassword:    operatorPassword,
		ClientSecret:        clientSecret,
		AllowedRedirectURIs: redirects,
		CodeTTL:             time.Minute,
		AccessTokenTTL:      5 * time.Minute,
		OpsSessionTTL:       8 * time.Hour,
	}, fmt.Sprintf(":%d", port), nil
}

func splitNonEmpty(raw string) []string {
	values := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}
