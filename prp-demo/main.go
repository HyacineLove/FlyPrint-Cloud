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
	log.Printf("PRP Demo listening on %s", address)
	log.Fatal(http.ListenAndServe(address, newServer(config, verifier).Handler()))
}
