package contrabass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleResponse = `{
  "returnCode": "200",
  "returnMessage": "COMMON_OK",
  "data": {
    "uuid": "66da2e07-a09d-4797-b9c6-75a2ff91381e",
    "url": "https://172.168.30.30:15000",
    "attributes": {
      "adminId": "admin",
      "adminPw": "dJsHjBidm1Egme0pkbTPUx6DuWoEs0tjBI+GPC34AaQPC34aCqfUBl0PKYuQAZXk",
      "domain": "default",
      "prometheusUrl": "https://172.168.30.30:9090",
      "vIp": "https://172.168.30.30:28888",
      "rabbitMQId": "openstack",
      "rabbitMQPw": "L/8l0UVhuCzYgfSfdKxV4mD5GFaDJbSRK0b42y76paM=",
      "rabbitMQUrls": [
        "172.168.30.30:5672"
      ]
    }
  }
}`

func TestGetProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/contrabass/admin/infra/provider/test-provider" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "conbaEncrypt2025", 5*time.Second)
	got, err := c.GetProvider(context.Background(), "test-provider")
	if err != nil {
		t.Fatalf("GetProvider error: %v", err)
	}

	if got.KeystoneURL != "https://172.168.30.30:15000" {
		t.Fatalf("KeystoneURL mismatch: %s", got.KeystoneURL)
	}
	if got.AdminID != "admin" || got.AdminPass != "CloudExpert2025!" {
		t.Fatalf("admin creds mismatch: %s/%s", got.AdminID, got.AdminPass)
	}
	if got.RabbitUser != "openstack" || got.RabbitPass != "wotjfcl1013!" {
		t.Fatalf("rabbit creds mismatch: %s/%s", got.RabbitUser, got.RabbitPass)
	}
	if got.Domain != "default" {
		t.Fatalf("domain mismatch: %s", got.Domain)
	}
	if len(got.RabbitURLs) != 1 || got.RabbitURLs[0] != "172.168.30.30:5672" {
		t.Fatalf("rabbit urls mismatch: %#v", got.RabbitURLs)
	}
}
