package artifacts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/sirupsen/logrus"

	"picotera/pkg/configx"
)

func TestBucketLookup(t *testing.T) {
	trueValue := true
	falseValue := false
	tests := []struct {
		name      string
		pathStyle *bool
		want      minio.BucketLookupType
	}{
		{name: "auto", pathStyle: nil, want: minio.BucketLookupAuto},
		{name: "path", pathStyle: &trueValue, want: minio.BucketLookupPath},
		{name: "dns", pathStyle: &falseValue, want: minio.BucketLookupDNS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bucketLookup(tt.pathStyle); got != tt.want {
				t.Fatalf("bucketLookup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewSinkAllowsEmptyPublicURL(t *testing.T) {
	sink, err := NewSink(configx.S3Config{
		Endpoint:  "localhost:34050",
		Region:    "us-east-1",
		AccessKey: "test-access",
		SecretKey: "test-secret",
		Bucket:    "picotera-artifacts",
		UseSSL:    false,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewSink() error = %v", err)
	}
	if !sink.Enabled() {
		t.Fatal("sink is disabled")
	}
}

func TestPresignedGetPublicVirtualHostedUsesFinalPublicURL(t *testing.T) {
	s := &minioSink{
		bucket:    "tokens-artifacts",
		accessKey: "test-access",
		secretKey: "test-secret",
		region:    "cn-beijing",
		publicURL: "https://tokens-artifacts.tos-s3-cn-beijing.volces.com",
	}

	got, err := s.presignedGetPublicVirtualHosted("artifacts/2026-06-24/d8.response.json.zst", time.Hour)
	if err != nil {
		t.Fatalf("presignedGetPublicVirtualHosted() error = %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	if u.Host != "tokens-artifacts.tos-s3-cn-beijing.volces.com" {
		t.Fatalf("host = %q", u.Host)
	}
	if strings.Contains(u.Host, "tokens-artifacts.tokens-artifacts") {
		t.Fatalf("host contains duplicated bucket: %q", u.Host)
	}
	if u.Path != "/artifacts/2026-06-24/d8.response.json.zst" {
		t.Fatalf("path = %q", u.Path)
	}
	if strings.Contains(u.Path, "/tokens-artifacts/") {
		t.Fatalf("path contains bucket: %q", u.Path)
	}

	q := u.Query()
	gotSig := q.Get("X-Amz-Signature")
	if gotSig == "" {
		t.Fatal("missing signature")
	}
	amzDate := q.Get("X-Amz-Date")
	if len(amzDate) != len("20260624T154318Z") {
		t.Fatalf("amz date = %q", amzDate)
	}
	date := amzDate[:8]
	if q.Get("X-Amz-Credential") != "test-access/"+date+"/cn-beijing/s3/aws4_request" {
		t.Fatalf("credential = %q", q.Get("X-Amz-Credential"))
	}
	q.Del("X-Amz-Signature")

	canonicalRequest := strings.Join([]string{
		"GET",
		"/artifacts/2026-06-24/d8.response.json.zst",
		testCanonicalQuery(q),
		"host:tokens-artifacts.tos-s3-cn-beijing.volces.com",
		"",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	scope := date + "/cn-beijing/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	wantSig := testHMACSHA256Hex(testSigningKey("test-secret", date, "cn-beijing", "s3"), stringToSign)
	if gotSig != wantSig {
		t.Fatalf("signature = %q, want %q", gotSig, wantSig)
	}
}

func testCanonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for key := range q {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	var parts []string
	for _, key := range keys {
		values := append([]string(nil), q[key]...)
		for i := 1; i < len(values); i++ {
			for j := i; j > 0 && values[j] < values[j-1]; j-- {
				values[j], values[j-1] = values[j-1], values[j]
			}
		}
		for _, value := range values {
			parts = append(parts, testSigV4Escape(key)+"="+testSigV4Escape(value))
		}
	}
	return strings.Join(parts, "&")
}

func testSigV4Escape(s string) string {
	var b strings.Builder
	const hexChars = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexChars[c>>4])
		b.WriteByte(hexChars[c&15])
	}
	return b.String()
}

func testSigningKey(secretKey, date, region, service string) []byte {
	kDate := testHMACSHA256([]byte("AWS4"+secretKey), date)
	kRegion := testHMACSHA256(kDate, region)
	kService := testHMACSHA256(kRegion, service)
	return testHMACSHA256(kService, "aws4_request")
}

func testHMACSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func testHMACSHA256Hex(key []byte, data string) string {
	return hex.EncodeToString(testHMACSHA256(key, data))
}
