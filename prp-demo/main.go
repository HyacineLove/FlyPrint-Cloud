package main

import (
	"log"
	"net/http"
)

func main() {
	config, address, err := configurationFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	verifier := tokenVerifier{
		secret:         config.TokenSecret,
		issuer:         config.TokenIssuer,
		audience:       config.TokenAudience,
		sitePortalCode: config.SitePortalCode,
	}
	server, err := newServer(config, verifier)
	if err != nil {
		log.Fatal(err)
	}
	defer server.close()
	log.Printf("PRP Demo listening on %s", address)
	log.Fatal(http.ListenAndServe(address, server.Handler()))
}
