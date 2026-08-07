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
		sitePortalCode: config.Code, clientID: config.CloudOAuthClientID,
		clientSecret: config.CloudOAuthClientSecret, client: client,
	}
	identity := &identityClient{
		tokenURL:      config.IdentityTokenURL,
		userinfoURL:   config.IdentityUserinfoURL,
		clientID:      config.IdentityClientID,
		clientSecret:  config.IdentityClientSecret,
		redirectURI:   config.IdentityCallbackURL,
		profileFormat: config.IdentityProfileFormat,
		opsAPIBaseURL: strings.TrimRight(config.IdentityOpsAPIBaseURL, "/"),
		client:        client,
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
		Code:                     strings.TrimSpace(os.Getenv("SITE_PORTAL_CODE")),
		DisplayName:              strings.TrimSpace(os.Getenv("SITE_PORTAL_DISPLAY_NAME")),
		CloudAPIBaseURL:          strings.TrimSpace(os.Getenv("SITE_PORTAL_CLOUD_API_BASE")),
		CloudOAuthClientID:       strings.TrimSpace(os.Getenv("SITE_PORTAL_CLOUD_CLIENT_ID")),
		CloudOAuthClientSecret:   os.Getenv("SITE_PORTAL_CLOUD_CLIENT_SECRET"),
		IdentityAuthorizationURL: strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_AUTHORIZATION_URL")),
		IdentityTokenURL:         strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_TOKEN_URL")),
		IdentityUserinfoURL:      strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_USERINFO_URL")),
		IdentityClientID:         strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_CLIENT_ID")),
		IdentityClientSecret:     os.Getenv("SITE_PORTAL_IDENTITY_CLIENT_SECRET"),
		IdentityScope:            strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_SCOPE")),
		IdentityProfileFormat:    strings.ToLower(strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_PROFILE_FORMAT"))),
		IdentityCallbackURL:      strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_CALLBACK_URL")),
		IdentityOpsAPIBaseURL:    strings.TrimSpace(os.Getenv("SITE_PORTAL_IDENTITY_OPS_API_BASE_URL")),
		PRPBaseURL:               strings.TrimSpace(os.Getenv("SITE_PORTAL_PRP_BASE_URL")),
		PRPAPIBaseURL:            strings.TrimSpace(os.Getenv("SITE_PORTAL_PRP_API_BASE_URL")),
		UploadEnabled:            uploadEnabled,
		LoginStateTTL:            5 * time.Minute,
		ClaimTTL:                 5 * time.Minute,
		OpsSessionTTL:            8 * time.Hour,
		UserSessionTTL:           userSessionTTL,
		CookieSecure:             strings.EqualFold(os.Getenv("SITE_PORTAL_COOKIE_SECURE"), "true"),
	}
	if config.IdentityScope == "" {
		config.IdentityScope = "ECNU-Basic"
	}
	if config.IdentityProfileFormat == "" {
		config.IdentityProfileFormat = "legacy"
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
