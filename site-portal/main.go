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
	config, address, err := portalConfigurationFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	cloud := &cloudClient{
		baseURL:        strings.TrimRight(config.CloudAPIBaseURL, "/"),
		sitePortalCode: config.Code, apiToken: config.CloudAPIToken, client: client,
	}
	identity := &identityClient{
		apiBaseURL:   strings.TrimRight(config.IdentityAPIBaseURL, "/"),
		clientSecret: config.IdentityClientSecret, client: client,
	}
	server := newPortalServer(config, cloud, identity)
	server.prp = &prpClient{
		baseURL: strings.TrimRight(config.PRPAPIBaseURL, "/"),
		client:  client,
	}
	log.Printf("Site Portal listening on %s", address)
	log.Fatal(http.ListenAndServe(address, server.Handler()))
}

func portalConfigurationFromEnvironment() (configuration, string, error) {
	uploadEnabledRaw := strings.TrimSpace(os.Getenv("SITE_PORTAL_UPLOAD_ENABLED"))
	uploadEnabled, err := strconv.ParseBool(uploadEnabledRaw)
	if err != nil {
		return configuration{}, "", fmt.Errorf("SITE_PORTAL_UPLOAD_ENABLED is invalid")
	}
	userSessionTTL, err := time.ParseDuration(strings.TrimSpace(os.Getenv("SITE_PORTAL_USER_SESSION_TTL")))
	if err != nil || userSessionTTL <= 0 {
		return configuration{}, "", fmt.Errorf("SITE_PORTAL_USER_SESSION_TTL is invalid")
	}
	config := configuration{
		Code:                   strings.TrimSpace(os.Getenv("SITE_PORTAL_CODE")),
		DisplayName:            strings.TrimSpace(os.Getenv("SITE_PORTAL_DISPLAY_NAME")),
		CloudAPIBaseURL:        strings.TrimSpace(os.Getenv("SITE_PORTAL_CLOUD_API_BASE")),
		CloudAPIToken:          os.Getenv("SITE_PORTAL_CLOUD_API_TOKEN"),
		IdentityBrowserBaseURL: strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_BROWSER_BASE_URL")),
		IdentityAPIBaseURL:     strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_API_BASE_URL")),
		IdentityClientSecret:   os.Getenv("SITE_PORTAL_IDENTITY_CLIENT_SECRET"),
		IdentityCallbackURL:    strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_CALLBACK_URL")),
		PRPBaseURL:             strings.TrimSpace(os.Getenv("SITE_PORTAL_PRP_BASE_URL")),
		PRPAPIBaseURL:          strings.TrimSpace(os.Getenv("SITE_PORTAL_PRP_API_BASE_URL")),
		UploadEnabled:          uploadEnabled,
		LoginStateTTL:          5 * time.Minute,
		ClaimTTL:               5 * time.Minute,
		OpsSessionTTL:          8 * time.Hour,
		UserSessionTTL:         userSessionTTL,
		CookieSecure:           strings.EqualFold(os.Getenv("SITE_PORTAL_COOKIE_SECURE"), "true"),
	}
	if err := config.validate(); err != nil {
		return configuration{}, "", err
	}
	port := 8080
	if raw := strings.TrimSpace(os.Getenv("SITE_PORTAL_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return configuration{}, "", fmt.Errorf("SITE_PORTAL_PORT is invalid")
		}
		port = parsed
	}
	return config, fmt.Sprintf(":%d", port), nil
}
