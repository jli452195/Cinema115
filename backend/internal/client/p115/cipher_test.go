package p115

import "testing"

func TestAESCBCRoundTrip(t *testing.T) {
	want := []byte("cinema115 p115 cipher test payload")
	ciphertext, err := AESEncryptCBC(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AESDecryptCBC(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("round trip mismatch: got %q, want %q", got, want)
	}
}

func TestAESDecryptRejectsInvalidLength(t *testing.T) {
	if _, err := AESDecryptCBC([]byte("short")); err == nil {
		t.Fatal("expected invalid length error")
	}
}
