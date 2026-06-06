package bedrock

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSHA256Hex(t *testing.T) {
	// Well-known SHA-256 of the empty string.
	if got := sha256Hex(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("sha256 empty = %s", got)
	}
}

func TestDeriveSigningKeyDeterministic(t *testing.T) {
	a := deriveSigningKey("secret", "20240101", "us-east-1", "bedrock")
	b := deriveSigningKey("secret", "20240101", "us-east-1", "bedrock")
	if string(a) != string(b) {
		t.Fatal("signing key not deterministic")
	}
	c := deriveSigningKey("secret", "20240102", "us-east-1", "bedrock")
	if string(a) == string(c) {
		t.Fatal("signing key should change with date")
	}
}

func TestSignV4Structure(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/m/invoke", nil)
	req.Header.Set("Content-Type", "application/json")
	creds := awsCreds{accessKeyID: "AKID", secretAccessKey: "SECRET", sessionToken: "TOKEN"}
	tm := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	signV4(req, []byte(`{"a":1}`), creds, "us-east-1", "bedrock", tm)

	if got := req.Header.Get("X-Amz-Date"); got != "20240102T030405Z" {
		t.Fatalf("X-Amz-Date = %q", got)
	}
	if req.Header.Get("X-Amz-Security-Token") != "TOKEN" {
		t.Fatal("session token header missing")
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, sigAlgorithm+" Credential=AKID/20240102/us-east-1/bedrock/aws4_request") {
		t.Fatalf("bad credential scope: %s", auth)
	}
	if !strings.Contains(auth, "SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-security-token") {
		t.Fatalf("bad signed headers: %s", auth)
	}
	i := strings.Index(auth, "Signature=")
	if i < 0 || len(auth[i+len("Signature="):]) != 64 {
		t.Fatalf("signature not 64 hex chars: %s", auth)
	}
}

func TestSignV4Deterministic(t *testing.T) {
	mk := func() *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.eu-west-1.amazonaws.com/model/x/invoke", nil)
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	creds := awsCreds{accessKeyID: "AKID", secretAccessKey: "SECRET"}
	tm := time.Date(2024, 6, 6, 7, 8, 9, 0, time.UTC)
	a := mk()
	b := mk()
	signV4(a, []byte("body"), creds, "eu-west-1", "bedrock", tm)
	signV4(b, []byte("body"), creds, "eu-west-1", "bedrock", tm)
	if a.Header.Get("Authorization") != b.Header.Get("Authorization") {
		t.Fatal("signature not deterministic for fixed inputs")
	}
}
