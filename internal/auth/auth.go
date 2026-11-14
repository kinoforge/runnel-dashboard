package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

const HashCost = 12

var ErrPasswordLength = errors.New("password must be 12 to 72 bytes")

type Users struct {
	hashes map[string][]byte
	dummy  []byte
}

func NewUsers(hashes map[string]string) *Users {
	u := &Users{hashes: map[string][]byte{}}
	cost := bcrypt.MinCost
	for name, h := range hashes {
		u.hashes[name] = []byte(h)
		if c, err := bcrypt.Cost([]byte(h)); err == nil {
			cost = max(cost, c)
		}
	}
	u.dummy, _ = bcrypt.GenerateFromPassword([]byte("runnel-dashboard-dummy-password"), cost)
	return u
}

func (u *Users) Check(name, password string) bool {
	h, ok := u.hashes[name]
	if !ok {
		_ = bcrypt.CompareHashAndPassword(u.dummy, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword(h, []byte(password)) == nil
}

func Hash(password string) (string, error) {
	if len(password) < 12 || len(password) > 72 {
		return "", ErrPasswordLength
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), HashCost)
	return string(h), err
}
