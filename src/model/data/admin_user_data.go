package data

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/mdb"
	"github.com/dromara/carbon/v2"
	"golang.org/x/crypto/bcrypt"
)

const defaultAdminUsername = "admin"

// EnsureDefaultAdmin seeds an initial admin account when no admin user
// exists. The password is randomly generated and returned so the caller
// can print it to the console. Idempotent — subsequent calls return
// ("", false, nil).
func EnsureDefaultAdmin() (password string, created bool, err error) {
	var count int64
	if err := dao.Mdb.Model(&mdb.AdminUser{}).Count(&count).Error; err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}
	password = randomAdminPassword()
	hash, err := HashPassword(password)
	if err != nil {
		return "", false, err
	}
	user := &mdb.AdminUser{
		Username:     defaultAdminUsername,
		PasswordHash: hash,
		Status:       mdb.AdminUserStatusEnable,
	}
	return password, true, dao.Mdb.Create(user).Error
}

func randomAdminPassword() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HashPassword bcrypts a plaintext password.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword compares a plaintext password against a bcrypt hash.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// GetAdminUserByUsername returns the row for a username (case-insensitive).
func GetAdminUserByUsername(username string) (*mdb.AdminUser, error) {
	u := new(mdb.AdminUser)
	err := dao.Mdb.Model(u).
		Where("username = ?", strings.ToLower(strings.TrimSpace(username))).
		Limit(1).Find(u).Error
	return u, err
}

// GetAdminUserByID returns the row for an ID.
func GetAdminUserByID(id uint64) (*mdb.AdminUser, error) {
	u := new(mdb.AdminUser)
	err := dao.Mdb.Model(u).Limit(1).Find(u, id).Error
	return u, err
}

// UpdateAdminUserPassword rehashes and persists a new password.
func UpdateAdminUserPassword(id uint64, newPlain string) error {
	hash, err := HashPassword(newPlain)
	if err != nil {
		return err
	}
	return dao.Mdb.Model(&mdb.AdminUser{}).
		Where("id = ?", id).
		Update("password_hash", hash).Error
}

// TouchAdminUserLastLogin stamps last_login_at to now.
func TouchAdminUserLastLogin(id uint64) error {
	return dao.Mdb.Model(&mdb.AdminUser{}).
		Where("id = ?", id).
		Update("last_login_at", carbon.Now().StdTime()).Error
}
