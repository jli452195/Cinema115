package p115

import (
	"context"
	"net/http"
	"testing"
)

func TestDirectoryMissingRequiresExactEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		status           int
		missing, wantErr bool
	}{
		{"missing", `{"state":false,"code":20018}`, 200, true, false},
		{"exists", `{"state":true,"data":[{"cid":"900"}]}`, 200, false, false},
		{"empty-success", `{"state":true,"data":[]}`, 200, false, false},
		{"login", `{"state":false,"errno":990001}`, 200, false, true},
		{"conflict", `{"state":false,"code":20018,"errno":99}`, 200, false, true},
		{"404", `{"state":false,"code":20018}`, 404, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestShareClient()
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/files/get_info" || r.URL.Query().Get("file_id") != "900" || r.Header.Get("Cookie") != "UID=22_A" {
					t.Fatal("wrong identity query")
				}
				return instantResponse(tc.status, []byte(tc.body)), nil
			})
			missing, err := client.DirectoryMissing(context.Background(), "900", "UID=22_A")
			if missing != tc.missing || (err != nil) != tc.wantErr {
				t.Fatalf("missing=%v err=%v", missing, err)
			}
		})
	}
}
