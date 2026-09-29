package main

import (
	"errors"
	"github.com/xiaoliu-heng/homefleet/internal/store"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// First startup and explicit password resets share the same validation. An
// invalid replacement must leave the existing password and sessions untouched.
func setAdminPassword(s *store.Store, password string) error {
	if len(password) < 12 || len(password) > 72 || strings.TrimSpace(password) == "" {
		return errors.New("set HOMEFLEET_ADMIN_PASSWORD to a unique password of 12–72 bytes")
	}
	normalized := strings.ToLower(strings.TrimSpace(password))
	if strings.HasPrefix(normalized, "replace-with-") || normalized == "自选的12至72字节密码" {
		return errors.New("HOMEFLEET_ADMIN_PASSWORD is an example placeholder; choose your own password")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO meta(k,v) VALUES('password',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", string(hash)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	return tx.Commit()
}
