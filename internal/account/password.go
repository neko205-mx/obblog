package account

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const iterations = 600000

func Hash(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("密码需为 12 至 1024 字节")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256$" + strconv.Itoa(iterations) + "$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

func decode(encoded string) (int, []byte, []byte, error) {
	p := strings.Split(encoded, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return 0, nil, nil, errors.New("无效的密码摘要")
	}
	n, err := strconv.Atoi(p[1])
	if err != nil || n < iterations || n > 2000000 {
		return 0, nil, nil, errors.New("无效的密码迭代次数")
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[2])
	key, e2 := base64.RawStdEncoding.DecodeString(p[3])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(key) != 32 {
		return 0, nil, nil, errors.New("无效的密码摘要")
	}
	return n, salt, key, nil
}

func Valid(encoded string) bool { _, _, _, err := decode(encoded); return err == nil }
func Verify(encoded, password string) bool {
	n, salt, want, err := decode(encoded)
	if err != nil || len(password) > 1024 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, n, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
