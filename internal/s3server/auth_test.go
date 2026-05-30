package s3server

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	awscredentials "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/signer/v4"
)

func TestAuthenticateRequestAcceptsAWSV4ListBuckets(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://localhost:9000/", nil)
	if err != nil {
		t.Fatal(err)
	}

	signer := v4.NewSigner(awscredentials.NewStaticCredentials("test-access", "test-secret", ""))
	_, err = signer.Sign(req, nil, "s3", "us-east-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := authenticateRequest(req, "test-access", "test-secret", nil); !ok {
		t.Fatal("expected root ListBuckets request to authenticate")
	}
}

func TestAuthenticateRequestCanonicalizesQueryLikeAWS(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://localhost:9000/test-bucket", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.URL.RawQuery = url.Values{
		"prefix":    {"folder name/with spaces"},
		"list-type": {"2"},
		"marker":    {"a+b"},
	}.Encode()

	signer := v4.NewSigner(awscredentials.NewStaticCredentials("test-access", "test-secret", ""))
	_, err = signer.Sign(req, nil, "s3", "us-east-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := authenticateRequest(req, "test-access", "test-secret", nil); !ok {
		t.Fatal("expected query-signed request to authenticate")
	}
}
