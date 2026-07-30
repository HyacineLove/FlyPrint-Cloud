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
	prpTokenSecret := os.Getenv("SSO_PRP_TOKEN_SECRET")
	prpTokenIssuer := strings.TrimSpace(os.Getenv("SSO_PRP_TOKEN_ISSUER"))
	prpTokenAudience := strings.TrimSpace(os.Getenv("SSO_PRP_TOKEN_AUDIENCE"))
	sitePortalCode := strings.TrimSpace(os.Getenv("SSO_SITE_PORTAL_CODE"))
	if dataFile == "" || operatorUsername == "" || operatorPassword == "" || clientSecret == "" ||
		len(redirects) == 0 || prpTokenSecret == "" || prpTokenIssuer == "" ||
		prpTokenAudience == "" || sitePortalCode == "" {
		return configuration{}, "", fmt.Errorf("SSO identity and PRP token configuration is required")
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
		PRPToken: prpTokenConfig{
			Secret:         prpTokenSecret,
			Issuer:         prpTokenIssuer,
			Audience:       prpTokenAudience,
			SitePortalCode: sitePortalCode,
			Scopes:         []string{"files:list", "files:download", "upload-context:create"},
		},
		CodeTTL:        time.Minute,
		AccessTokenTTL: 5 * time.Minute,
		OpsSessionTTL:  8 * time.Hour,
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
