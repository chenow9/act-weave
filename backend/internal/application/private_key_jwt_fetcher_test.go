package application

import (
	"context"
	"net"
	"testing"
	"time"

	"actweave/backend/internal/agentaccessauth"
)

func TestPrivateKeyJWTAuthenticationJWKSFetcherRejectsSSRF(t *testing.T) {
	fetcher := privateKeyJWTJWKSFetcher{resolver: privateKeyJWTLoopbackResolver{}}
	for name, target := range map[string]string{
		"plain HTTP":       "http://keys.example.test/jwks",
		"loopback literal": "https://127.0.0.1/jwks",
		"link local":       "https://169.254.169.254/latest/meta-data",
		"DNS rebinding":    "https://keys.example.test/jwks",
		"userinfo":         "https://user@keys.example.test/jwks",
		"fragment":         "https://keys.example.test/jwks#private",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fetcher.FetchRemoteJWKS(context.Background(), target,
				agentaccessauth.DefaultRemoteJWKSMaxBytes); err == nil {
				t.Fatalf("unsafe JWKS target was accepted: %s", target)
			}
		})
	}
}

func TestPrivateKeyJWTAuthenticationJWKSCacheControlParsing(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"public, max-age=60":   time.Minute,
		`max-age="0"`:          0,
		"private, max-age=900": 15 * time.Minute,
		"no-store":             0,
	} {
		got, found := jwksCacheMaxAge(value)
		if !found || got != want {
			t.Fatalf("Cache-Control %q parsed duration=%s found=%t want=%s", value, got, found, want)
		}
	}
	if _, found := jwksCacheMaxAge("public"); found {
		t.Fatal("Cache-Control without a freshness directive must use the bounded default")
	}
}

type privateKeyJWTLoopbackResolver struct{}

func (privateKeyJWTLoopbackResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}
