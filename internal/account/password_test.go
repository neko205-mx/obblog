package account

import "testing"

func TestPasswordHash(t *testing.T) {
	a, err := Hash("temporary-password-123")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash("temporary-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !Valid(a) || !Verify(a, "temporary-password-123") || Verify(a, "wrong-password") {
		t.Fatal("password hashing/verification failed")
	}
	if _, err := Hash("short"); err == nil {
		t.Fatal("short password accepted")
	}
	for _, encoded := range []string{"plaintext", "pbkdf2-sha256$99999999999$a$b", "pbkdf2-sha256$600000$a$b"} {
		if Valid(encoded) || Verify(encoded, "x") {
			t.Fatal("invalid encoding accepted")
		}
	}
}
