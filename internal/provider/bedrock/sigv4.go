package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// awsCreds holds the credentials used to sign a request.
type awsCreds struct {
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
}

const sigAlgorithm = "AWS4-HMAC-SHA256"

// signV4 signs an HTTP request with AWS Signature Version 4 in place, adding the
// X-Amz-Date, (optional) X-Amz-Security-Token and Authorization headers. The
// payload must be the exact request body bytes.
func signV4(req *http.Request, payload []byte, creds awsCreds, region, service string, t time.Time) {
	amzDate := t.UTC().Format("20060102T150405Z")
	dateStamp := t.UTC().Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	if creds.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.sessionToken)
	}
	host := req.URL.Host
	req.Header.Set("Host", host)

	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders, canonicalHeaders := canonicalHeaders(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req),
		canonicalQuery(req),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		sigAlgorithm,
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(creds.secretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	auth := sigAlgorithm +
		" Credential=" + creds.accessKeyID + "/" + scope +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + signature
	req.Header.Set("Authorization", auth)
}

// canonicalHeaders returns the SignedHeaders list and the canonical headers
// block for the headers ties always sets.
func canonicalHeaders(req *http.Request) (signed, canonical string) {
	include := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("X-Amz-Security-Token") != "" {
		include = append(include, "x-amz-security-token")
	}
	if req.Header.Get("Content-Type") != "" {
		include = append(include, "content-type")
	}
	sort.Strings(include)

	var b strings.Builder
	for _, h := range include {
		var v string
		switch h {
		case "host":
			v = req.URL.Host
		default:
			v = req.Header.Get(h)
		}
		b.WriteString(h)
		b.WriteString(":")
		b.WriteString(strings.TrimSpace(v))
		b.WriteString("\n")
	}
	return strings.Join(include, ";"), b.String()
}

func canonicalURI(req *http.Request) string {
	p := req.URL.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}

func canonicalQuery(req *http.Request) string {
	return req.URL.Query().Encode()
}

func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
