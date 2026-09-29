package main

import (
	"github.com/xiaoliu-heng/homefleet/internal/store"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestInvalidPasswordNeverInitializesOrResetsCredentials(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, existing := range []string{"", "existing-hash"} {
		if err := s.SetMeta("password", existing); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec("INSERT OR REPLACE INTO sessions VALUES('existing-session','2099-01-01T00:00:00Z')"); err != nil {
			t.Fatal(err)
		}
		for _, password := range []string{"", "short", strings.Repeat("x", 73), strings.Repeat("界", 25), strings.Repeat(" ", 16), "replace-with-a-unique-long-password", "replace-with-your-password", " REPLACE-WITH-YOUR-PASSWORD ", "自选的12至72字节密码"} {
			if err := setAdminPassword(s, password); err == nil {
				t.Fatal("invalid password accepted")
			}
			var sessions int
			if err := s.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if s.Meta("password") != existing || sessions != 1 {
				t.Fatal("invalid password changed credentials or sessions")
			}
		}
	}
}

func TestPasswordInitializationAndResetHashAndInvalidateSessions(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, password := range []string{"test-first-admin-pass", "test-reset-admin-pass"} {
		if _, err := s.DB.Exec("INSERT INTO sessions VALUES('existing-session','2099-01-01T00:00:00Z')"); err != nil {
			t.Fatal(err)
		}
		if err := setAdminPassword(s, password); err != nil {
			t.Fatal(err)
		}
		if hash := s.Meta("password"); hash == password || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
			t.Fatal("password was not hashed correctly")
		}
		var sessions int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
			t.Fatal("password change did not invalidate sessions", err)
		}
	}
}
