package p115

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestRSAUpstreamVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/rsa_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input     string
		Encrypted string
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, item := range cases {
		input, err := base64.StdEncoding.DecodeString(item.Input)
		if err != nil {
			t.Fatal(err)
		}
		if RSAEncrypt(input) != item.Encrypted {
			t.Fatalf("长度 %d 与 p115cipher 输出不一致", len(input))
		}
	}
}

func TestRSARejectsMalformedBlocks(t *testing.T) {
	for _, data := range []string{"!", "", base64.StdEncoding.EncodeToString(make([]byte, 127)), base64.StdEncoding.EncodeToString(make([]byte, 128))} {
		if _, err := RSADecrypt(data); err == nil {
			t.Fatal("无效密文被接受")
		}
	}
}
