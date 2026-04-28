package admin

import "testing"

func TestHashPasswordAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("hash must not equal plaintext password")
	}
	if err := CheckPassword(hash, "correct horse battery staple"); err != nil {
		t.Fatalf("CheckPassword returned error for correct password: %v", err)
	}
	if err := CheckPassword(hash, "wrong password"); err == nil {
		t.Fatal("CheckPassword accepted wrong password")
	}
}
